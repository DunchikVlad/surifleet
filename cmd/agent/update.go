package main

import (
	"bytes"
	"encoding/json"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// Параметры suricata-update (чанк 82).
const (
	updateStepTimeout  = 60 * time.Second  // enable/disable/list-sources
	updateRunTimeout   = 10 * time.Minute  // сам suricata-update (первая загрузка большая)
	updateUploadTimeout = 5 * time.Minute  // заливка итогового набора на сервер
	// updateOutputDefault — итоговый файл suricata-update по умолчанию.
	updateOutputDefault = "/var/lib/suricata/rules/suricata.rules"
)

// executeSuricataUpdate — задача suricata_update: enable/disable источников,
// list-sources, запуск обновления, опциональная заливка итогового набора на
// сервер (импорт в общий список) и reload движка.
func (e *taskExecutor) executeSuricataUpdate(task *agentv1.Task, su *agentv1.SuricataUpdateTask) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID, "instance_id", su.GetInstanceId())

	if !e.hasCap("rules") {
		e.failTask(taskID, nil, "capability rules не включена для хоста")
		return
	}
	if cached, ok := e.lookupProcessed(taskID); ok {
		log.Info("задача уже обработана — повторяю сохранённый результат", "status", cached.Status)
		e.reply(&agentv1.TaskResult{TaskId: taskID, Status: protoStatus(cached.Status), Error: cached.Error})
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.failTask(taskID, nil, "дедлайн задачи истёк")
		return
	}

	res := &agentv1.SuricataUpdateResult{UploadKey: su.GetUploadKey(), SourcesKey: su.GetSourcesKey()}
	run := func(name string, args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), updateStepTimeout)
		defer cancel()
		out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
		return string(out), err
	}

	// 1. enable/disable источников (конфиг update.yaml на сенсоре).
	var opErrs []string
	for _, src := range su.GetEnableSources() {
		out, err := run("suricata-update", "enable-source", src)
		log.Info("enable-source", "source", src, "err", err)
		if err != nil {
			opErrs = append(opErrs, "enable-source "+src+": "+err.Error()+" "+tail(out, 3))
		}
	}
	for _, src := range su.GetDisableSources() {
		out, err := run("suricata-update", "disable-source", src)
		log.Info("disable-source", "source", src, "err", err)
		if err != nil {
			opErrs = append(opErrs, "disable-source "+src+": "+err.Error()+" "+tail(out, 3))
		}
	}

	// 2. Список источников: all = list-sources, enabled = list-enabled-sources.
	sources, lerr := e.listUpdateSources()
	if lerr != nil {
		log.Warn("list-sources не получен", "err", lerr)
	}
	res.Sources = sources

	// 3. Собственно обновление (если не list-only и не no_update).
	needUpdate := !su.GetListSources() && !su.GetNoUpdate()
	if needUpdate {
		ctx, cancel := context.WithTimeout(context.Background(), updateRunTimeout)
		out, err := exec.CommandContext(ctx, "suricata-update").CombinedOutput()
		cancel()
		res.Output = tail(string(out), 30)
		if err != nil {
			e.failSuricataUpdate(taskID, res, "suricata-update: "+err.Error()+"; вывод: "+res.Output)
			return
		}
		data, rerr := os.ReadFile(updateOutputDefault)
		if rerr != nil {
			// вывод не по дефолту — ищем самый свежий .rules рядом.
			data, rerr = readNewestRules(filepath.Dir(updateOutputDefault))
		}
		if rerr != nil {
			e.failSuricataUpdate(taskID, res, "чтение итогового набора: "+rerr.Error())
			return
		}
		res.RulesCount = int32(len(parseSids(data)))

		// 4. Заливка итогового набора на сервер (импорт в общий список).
		if su.GetUploadUrl() != "" {
			if uerr := uploadFile(su.GetUploadUrl(), data); uerr != nil {
				e.failSuricataUpdate(taskID, res, "заливка набора на сервер: "+uerr.Error())
				return
			}
			res.UploadedBytes = int64(len(data))
			log.Info("итоговый набор залит на сервер", "bytes", len(data), "rules", res.RulesCount)
		}
		// Карта sid → источник (чанк 95): для выбора источников в авто-ruleset'ах.
		if su.GetSourcesUrl() != "" {
			surVer := ""
			if rep := e.disc.Load(); rep != nil {
				surVer = rep.GetBinary().GetVersion()
			}
			m := buildSidSourceMap(surVer)
			if mraw, merr := json.Marshal(m); merr == nil && len(m) > 0 {
				if uerr := uploadFile(su.GetSourcesUrl(), mraw); uerr != nil {
					log.Warn("заливка карты источников", "err", uerr)
				} else {
					res.SourcesBytes = int64(len(mraw))
					log.Info("карта sid → источник залита", "entries", len(m), "bytes", len(mraw))
				}
			}
		}

		// 5. Reload движка (опционально).
		if su.GetReload() {
			socket := defaultCommandSocket
			if out, rerr := suricatasc(socket, "reload-rules", reloadTimeout); rerr != nil {
				log.Warn("reload-rules неуспешен", "err", rerr, "вывод", tail(out, 5))
				res.Output += "\nreload-rules: " + rerr.Error()
			} else {
				res.Output += "\nreload-rules: ok"
			}
		}
	}

	if len(opErrs) > 0 {
		e.failSuricataUpdate(taskID, res, strings.Join(opErrs, "; "))
		return
	}
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: &agentv1.TaskResult_SuricataUpdate{SuricataUpdate: res},
	})
	if needUpdate {
		e.saveProcessed(taskID, cachedResult{Status: "succeeded"})
	}
	log.Info("suricata-update завершён", "rules", res.RulesCount, "sources", len(res.Sources))
}

// failSuricataUpdate — failed с деталями SuricataUpdateResult (без журнала:
// провал перевыполним, как и failTask).
func (e *taskExecutor) failSuricataUpdate(taskID string, res *agentv1.SuricataUpdateResult, msg string) {
	e.log.Warn("suricata-update завершился ошибкой", "task_id", taskID, "error", msg)
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_FAILED,
		Error:   msg,
		Details: &agentv1.TaskResult_SuricataUpdate{SuricataUpdate: res},
	})
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

var (
	listSrcNameRe    = regexp.MustCompile(`^Name:\s*(\S+)`)
	listSrcSummaryRe = regexp.MustCompile(`^\s+Summary:\s*(.+)$`)
)

// listUpdateSources — all-источники (list-sources) с пометкой enabled
// (list-enabled-sources). Вывод suricata-update цветной и блочный:
// «Name: <id>» + «  Summary: <text>»; перед списком — update-sources
// (обновление индекса, best-effort). Разбор защитный.
func (e *taskExecutor) listUpdateSources() ([]*agentv1.SourceInfo, error) {
	strip := func(out []byte) []string {
		clean := ansiRe.ReplaceAllString(string(out), "")
		var lines []string
		for _, ln := range strings.Split(clean, "\n") {
			ln = strings.TrimRight(ln, " 	")
			if strings.TrimSpace(ln) != "" && !strings.Contains(ln, " -- ") {
				lines = append(lines, ln)
			}
		}
		return lines
	}

	enabled := map[string]bool{}
	ctx, cancel := context.WithTimeout(context.Background(), updateStepTimeout)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "suricata-update", "list-enabled-sources").CombinedOutput(); err == nil {
		for _, ln := range strip(out) {
			if m := listSrcNameRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
				enabled[m[1]] = true
				continue
			}
			f := strings.Fields(ln)
			if len(f) > 1 && f[0] == "-" {
				enabled[f[1]] = true // формат «  - et/open»
			} else if len(f) > 0 && f[0] != "-" {
				enabled[strings.TrimSuffix(f[0], ":")] = true
			}
		}
	}

	ctx2, cancel2 := context.WithTimeout(context.Background(), updateStepTimeout)
	defer cancel2()
	out, err := exec.CommandContext(ctx2, "suricata-update", "list-sources").CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("list-sources: %w; вывод: %s", err, tail(string(out), 5))
	}
	// Индекса нет (свежий сенсор) — сначала update-sources, потом повтор.
	if bytes.Contains(out, []byte("Source index does not exist")) {
		ctx3, cancel3 := context.WithTimeout(context.Background(), 2*updateStepTimeout)
		_ = exec.CommandContext(ctx3, "suricata-update", "update-sources").Run()
		cancel3()
		ctx4, cancel4 := context.WithTimeout(context.Background(), updateStepTimeout)
		defer cancel4()
		out, err = exec.CommandContext(ctx4, "suricata-update", "list-sources").CombinedOutput()
		if err != nil {
			return nil, fmt.Errorf("list-sources (после update-sources): %w; вывод: %s", err, tail(string(out), 5))
		}
	}
	var res []*agentv1.SourceInfo
	byName := map[string]*agentv1.SourceInfo{}
	for _, ln := range strip(out) {
		trimmed := strings.TrimSpace(ln)
		if m := listSrcNameRe.FindStringSubmatch(trimmed); m != nil {
			si := &agentv1.SourceInfo{Name: m[1], Enabled: enabled[m[1]]}
			res = append(res, si)
			byName[si.Name] = si
			continue
		}
		if m := listSrcSummaryRe.FindStringSubmatch(ln); m != nil && len(res) > 0 {
			// Summary относится к последнему Name (блочный формат).
			if cur := byName[res[len(res)-1].Name]; cur != nil && cur.Summary == "" {
				cur.Summary = strings.TrimSpace(m[1])
			}
		}
	}
	sort.Slice(res, func(i, j int) bool { return res[i].GetName() < res[j].GetName() })
	return res, nil
}

// readNewestRules — самый свежий непустой .rules в каталоге (фолбэк, если
// итоговый файл suricata-update не по дефолтному пути).
func readNewestRules(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var best string
	var bestTime time.Time
	for _, en := range entries {
		if en.IsDir() || !strings.HasSuffix(en.Name(), ".rules") {
			continue
		}
		fi, err := en.Info()
		if err != nil {
			continue
		}
		if best == "" || fi.ModTime().After(bestTime) {
			best, bestTime = filepath.Join(dir, en.Name()), fi.ModTime()
		}
	}
	if best == "" {
		return nil, fmt.Errorf("нет .rules в %s", dir)
	}
	return os.ReadFile(best)
}

// uploadFile — PUT содержимого на presigned URL с контролем ответа.
func uploadFile(url string, data []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), updateUploadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "text/plain; charset=utf-8")
	req.ContentLength = int64(len(data))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, tail(string(body), 3))
	}
	// Целостность: сервер сверит sha256 по содержимому блоба при импорте.
	sum := sha256.Sum256(data)
	_ = hex.EncodeToString(sum[:])
	return nil
}
