package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// Параметры деплоя правил (chunk 11).
const (
	// managedRulesFile — имя файла правил под управлением SuriFleet.
	// При capability 'rules' SuriFleet полностью управляет секцией
	// rule-files в suricata.yaml: managed-файл добавляется, прочие
	// источники отключаются комментарием "# surifleet-disabled:"
	// (иначе правила из деплоя конфликтуют со штатными — Duplicate
	// signature). Все правки — с бэкапом yaml рядом (.surifleet-bak-TS).
	managedRulesFile = "zz-surifleet-managed.rules"
	// defaultCommandSocket — сокет unix-command Suricata по умолчанию
	// (переопределяется из unix-command.filename конфига инстанса).
	defaultCommandSocket = "/var/run/suricata/suricata-command.socket"

	downloadTimeout   = 60 * time.Second
	validateTimeout   = 120 * time.Second
	suricatascTimeout = 30 * time.Second
	// reloadTimeout — отдельный большой таймаут для reload-rules: движок
	// отвечает на команду только после фактической перезагрузки правил,
	// на нагруженном сенсоре это занимает десятки секунд (на стенде ~70 с).
	reloadTimeout = 240 * time.Second
	// reloadSettleDelay — пауза после reload-rules перед опросом
	// ruleset-failed-rules (движок догружает правила асинхронно).
	reloadSettleDelay = 3 * time.Second

	maxRulesetBytes = 64 << 20 // 64 МБ — защитный предел блоба
)

// taskExecutor — исполнитель задач сервера: capability-гейт, идемпотентность
// по task_id (журнал processed_tasks.jsonl), деплой правил end-to-end.
type taskExecutor struct {
	log     *slog.Logger
	dataDir string
	caps    map[string]bool
	send    func(*agentv1.AgentMessage) error

	mu        sync.Mutex
	processed map[string]cachedResult
	loaded    bool
}

// cachedResult — сохранённый результат задачи (для повторной отправки
// при дубле доставки: at-least-once → агент отвечает тем же результатом).
type cachedResult struct {
	Status string                     `json:"status"` // succeeded | failed | cancelled
	Error  string                     `json:"error,omitempty"`
	Deploy *agentv1.DeployRulesResult `json:"deploy,omitempty"`
}

// journalEntry — строка журнала обработанных задач (JSONL).
type journalEntry struct {
	TaskID string       `json:"task_id"`
	At     string       `json:"at"`
	Result cachedResult `json:"result"`
}

func newTaskExecutor(dataDir string, caps []string, send func(*agentv1.AgentMessage) error, log *slog.Logger) *taskExecutor {
	m := map[string]bool{}
	for _, c := range caps {
		m[c] = true
	}
	return &taskExecutor{log: log, dataDir: dataDir, caps: m, send: send, processed: map[string]cachedResult{}}
}

// handle разбирает задачу сервера. DeployRulesTask выполняется асинхронно
// (скачивание + валидация + reload — секунды), остальные типы — честный отказ.
func (e *taskExecutor) handle(task *agentv1.Task) {
	e.log.Info("получена задача", "task_id", task.GetTaskId(), "type", taskTypeName(task))
	dr := task.GetDeployRules()
	if dr == nil {
		e.reply(&agentv1.TaskResult{
			TaskId: task.GetTaskId(),
			Status: agentv1.TaskStatus_TASK_STATUS_FAILED,
			Error:  "тип задачи не поддерживается агентом: " + taskTypeName(task),
		})
		return
	}
	go e.executeDeploy(task, dr)
}

func taskTypeName(task *agentv1.Task) string {
	switch task.GetType().(type) {
	case *agentv1.Task_DeployRules:
		return "deploy_rules"
	case *agentv1.Task_DeployConfig:
		return "deploy_config"
	case *agentv1.Task_ServiceAction:
		return "service_action"
	case *agentv1.Task_Rollback:
		return "rollback"
	case *agentv1.Task_CollectBundle:
		return "collect_bundle"
	case *agentv1.Task_AgentUpdate:
		return "agent_update"
	case *agentv1.Task_SetCapabilities:
		return "set_capabilities"
	default:
		return "unknown"
	}
}

// executeDeploy — деплой ruleset end-to-end:
// скачивание по signed_url → сверка sha256 → ensure rule-files → бэкап →
// атомарная запись → suricata -T (откат при ошибке) → reload-rules →
// верификация ruleset-failed-rules → TaskResult + RuleLoadReport.
func (e *taskExecutor) executeDeploy(task *agentv1.Task, dr *agentv1.DeployRulesTask) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID, "instance_id", dr.GetInstanceId(), "ruleset", dr.GetRulesetVersion())

	// Capability-гейт: без rules хост не отдаёт управление правилами (ТЗ п.5).
	if !e.caps["rules"] {
		e.failAndJournal(taskID, nil, "capability rules не включена для хоста")
		return
	}
	// Идемпотентность: повторная доставка — повтор сохранённого результата.
	if cached, ok := e.lookupProcessed(taskID); ok {
		log.Info("задача уже обработана — повторяю сохранённый результат", "status", cached.Status)
		e.reply(&agentv1.TaskResult{
			TaskId:  taskID,
			Status:  protoStatus(cached.Status),
			Error:   cached.Error,
			Details: deployDetails(cached.Deploy),
		})
		return
	}
	// Дедлайн: после срока задача не выполняется (protocol: cancelled).
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.failAndJournal(taskID, nil, "дедлайн задачи истёк") // failed, не cancelled: сервер фиксирует невыполнение
		return
	}

	hash := strings.ToLower(dr.GetRulesetHash())

	// 1. Скачивание блоба и сверка целостности.
	data, err := downloadBlob(dr.GetSignedUrl())
	if err != nil {
		e.failAndJournal(taskID, nil, "скачивание ruleset: "+err.Error())
		return
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != hash {
		e.failAndJournal(taskID, nil, fmt.Sprintf("sha256 не совпал: ожидался %s, получен %s", hash, got))
		return
	}
	log.Info("ruleset скачан и проверен", "bytes", len(data), "sha256", hash)

	// 2. rule-files в suricata.yaml должен включать managed-файл (с бэкапом).
	if err := ensureRuleFiles(dr.GetConfigPath(), log); err != nil {
		e.failAndJournal(taskID, nil, "правка rule-files: "+err.Error())
		return
	}

	// 3. Бэкап текущего managed-файла и атомарная запись нового.
	target := filepath.Join(dr.GetRulesDir(), managedRulesFile)
	backup, hadBackup, err := backupFile(target)
	if err != nil {
		e.failAndJournal(taskID, nil, "бэкап managed-файла: "+err.Error())
		return
	}
	if err := writeFileAtomic(target, data, 0o644); err != nil {
		e.failAndJournal(taskID, nil, "запись managed-файла: "+err.Error())
		return
	}
	log.Info("managed-файл записан", "path", target, "backup", backup)

	// 4. Валидация конфигурации движком; при ошибке — откат файла.
	if out, err := validateConfig(dr.GetConfigPath()); err != nil {
		rollback(target, backup, hadBackup, log)
		e.failAndJournal(taskID, nil, "suricata -T: "+err.Error()+"; вывод: "+tail(out, 20))
		return
	}
	log.Info("suricata -T пройден")

	// 5. Перезагрузка правил движка через unix-command сокет.
	socket := commandSocket(dr.GetConfigPath())
	reloadOK := true
	reloadMsg := ""
	if out, err := suricatasc(socket, "reload-rules", reloadTimeout); err != nil {
		reloadOK = false
		reloadMsg = "reload-rules: " + err.Error() + "; вывод: " + tail(out, 5)
		log.Warn("reload-rules неуспешен", "err", err)
	} else {
		reloadMsg = "reload-rules: ok"
	}
	time.Sleep(reloadSettleDelay)

	// 6. Верификация фактической загрузки: ruleset-failed-rules.
	failed, ferr := failedRules(socket)
	if ferr != nil {
		log.Warn("ruleset-failed-rules не получен", "err", ferr)
	}
	fileRules := parseSids(data) // sid → rev из записанного файла
	failedSet := map[int64]bool{}
	failedRulesPB := make([]*agentv1.FailedRule, 0, len(failed))
	for _, f := range failed {
		failedSet[f.Sid] = true
		failedRulesPB = append(failedRulesPB, &agentv1.FailedRule{
			Sid: f.Sid, Rev: fileRules[f.Sid], ErrorText: f.Error,
		})
	}
	loadedPB := make([]*agentv1.LoadedRule, 0, len(fileRules))
	for sid, rev := range fileRules {
		if !failedSet[sid] {
			loadedPB = append(loadedPB, &agentv1.LoadedRule{Sid: sid, Rev: rev})
		}
	}
	sort.Slice(loadedPB, func(i, j int) bool { return loadedPB[i].GetSid() < loadedPB[j].GetSid() })

	deployRes := &agentv1.DeployRulesResult{
		RulesetHash: hash,
		LoadedCount: int32(len(loadedPB)),
		FailedCount: int32(len(failedRulesPB)),
	}
	now := timestamppb.Now()
	stateAfter := &agentv1.StateReport{
		InstanceId:  dr.GetInstanceId(),
		RulesetHash: hash,
		LoadedRules: loadedPB,
		FailedRules: failedRulesPB,
		ReportedAt:  now,
		Full:        true,
		LastReload: &agentv1.ReloadResult{
			Action:     agentv1.ServiceAction_SERVICE_ACTION_RELOAD,
			Success:    reloadOK,
			Message:    reloadMsg,
			FinishedAt: now,
		},
	}

	// Деплой успешен, если файл применён и движок перезагружен; частично
	// отклонённые правила — НЕ провал задачи (сервер посчитает partial
	// по actual state). Провал — невозможность применить (см. выше).
	if !reloadOK {
		e.failAndJournal(taskID, deployRes, reloadMsg)
		return
	}
	res := &agentv1.TaskResult{
		TaskId:     taskID,
		Status:     agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details:    &agentv1.TaskResult_DeployRules{DeployRules: deployRes},
		StateAfter: stateAfter,
	}
	e.reply(res)
	e.saveProcessed(taskID, cachedResult{Status: "succeeded", Deploy: deployRes})
	log.Info("деплой ruleset завершён",
		"loaded", len(loadedPB), "failed", len(failedRulesPB), "hash", hash)

	// Оперативный сигнал оркестратору отдельным сообщением.
	e.reply2(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_RuleLoadReport{RuleLoadReport: &agentv1.RuleLoadReport{
		InstanceId:  dr.GetInstanceId(),
		RulesetHash: hash,
		LoadedCount: int32(len(loadedPB)),
		FailedRules: failedRulesPB,
		VerifiedAt:  timestamppb.Now(),
	}}})
}

// reply отправляет TaskResult серверу.
func (e *taskExecutor) reply(res *agentv1.TaskResult) {
	e.reply2(&agentv1.AgentMessage{Payload: &agentv1.AgentMessage_TaskResult{TaskResult: res}})
}

func (e *taskExecutor) reply2(m *agentv1.AgentMessage) {
	if err := e.send(m); err != nil {
		e.log.Warn("сообщение серверу не отправлено", "err", err)
	}
}

// failAndJournal — TaskResult failed + запись в журнал (идемпотентность).
func (e *taskExecutor) failAndJournal(taskID string, deploy *agentv1.DeployRulesResult, msg string) {
	e.log.Warn("задача завершилась ошибкой", "task_id", taskID, "error", msg)
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_FAILED,
		Error:   msg,
		Details: deployDetails(deploy),
	})
	e.saveProcessed(taskID, cachedResult{Status: "failed", Error: msg, Deploy: deploy})
}

func deployDetails(d *agentv1.DeployRulesResult) *agentv1.TaskResult_DeployRules {
	if d == nil {
		return nil
	}
	return &agentv1.TaskResult_DeployRules{DeployRules: d}
}

func protoStatus(s string) agentv1.TaskStatus {
	switch s {
	case "succeeded":
		return agentv1.TaskStatus_TASK_STATUS_SUCCESS
	case "cancelled":
		return agentv1.TaskStatus_TASK_STATUS_CANCELLED
	default:
		return agentv1.TaskStatus_TASK_STATUS_FAILED
	}
}

// --- журнал обработанных задач (идемпотентность, переживает рестарт агента) ---

func (e *taskExecutor) journalPath() string {
	return filepath.Join(e.dataDir, "processed_tasks.jsonl")
}

// loadJournal лениво читает журнал в память (под e.mu).
func (e *taskExecutor) loadJournalLocked() {
	if e.loaded {
		return
	}
	e.loaded = true
	f, err := os.Open(e.journalPath())
	if err != nil {
		return // журнала ещё нет — нормально
	}
	defer f.Close()
	dec := json.NewDecoder(f)
	for {
		var je journalEntry
		if err := dec.Decode(&je); err != nil {
			return // EOF или битый хвост — берём что прочитали
		}
		e.processed[je.TaskID] = je.Result
	}
}

func (e *taskExecutor) lookupProcessed(taskID string) (cachedResult, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.loadJournalLocked()
	r, ok := e.processed[taskID]
	return r, ok
}

func (e *taskExecutor) saveProcessed(taskID string, r cachedResult) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.loadJournalLocked()
	if _, dup := e.processed[taskID]; dup {
		return
	}
	e.processed[taskID] = r
	if err := os.MkdirAll(e.dataDir, 0o755); err != nil {
		e.log.Warn("journal: mkdir", "err", err)
		return
	}
	f, err := os.OpenFile(e.journalPath(), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		e.log.Warn("journal: open", "err", err)
		return
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(journalEntry{
		TaskID: taskID,
		At:     time.Now().UTC().Format(time.RFC3339),
		Result: r,
	})
}

// --- скачивание и проверка блоба ---

func downloadBlob(url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), downloadTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRulesetBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxRulesetBytes {
		return nil, fmt.Errorf("блоб больше %d байт", maxRulesetBytes)
	}
	return data, nil
}

// --- правка suricata.yaml (rule-files) ---

// ensureRuleFiles гарантирует, что managed-файл присутствует в секции
// rule-files конфига и что он там ЕДИНСТВЕННЫЙ активный источник правил:
// при capability 'rules' SuriFleet берёт набор rule-files под полное
// управление — прочие элементы отключаются комментарием
// "# surifleet-disabled:" (идемпотентно, повторный вызов их не трогает).
// При изменении — бэкап yaml рядом (.surifleet-bak-TS).
func ensureRuleFiles(configPath string, log *slog.Logger) error {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}
	lines := strings.Split(string(raw), "\n")

	// Ищем секцию rule-files:, внутри неё — активные элементы списка.
	secIdx := -1
	lastItem := -1
	managedPresent := false
	disabled := 0
	for i, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if secIdx < 0 {
			if strings.HasPrefix(trimmed, "rule-files:") {
				secIdx = i
			}
			continue
		}
		// Внутри секции: элементы списка вида "  - file.rules".
		if strings.HasPrefix(trimmed, "- ") {
			entry := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "- ")), `"'`)
			if entry == managedRulesFile {
				managedPresent = true
				lastItem = i
				continue
			}
			// Отключаем чужой источник правил: оставляем строку
			// комментарием с маркером — обратимо и идемпотентно.
			indent := ln[:len(ln)-len(strings.TrimLeft(ln, " \t"))]
			lines[i] = indent + "# surifleet-disabled: " + trimmed
			log.Info("rule-files: источник отключён (управление у SuriFleet)", "entry", entry)
			disabled++
			continue
		}
		// Пустые строки/комментарии внутри списка допустимы (в т.ч. наши
		// "# surifleet-disabled:" от прошлых запусков).
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		break // вышли из секции
	}
	if secIdx < 0 {
		return fmt.Errorf("секция rule-files не найдена в %s", configPath)
	}
	if managedPresent && disabled == 0 {
		return nil // уже подключён и единственный активный
	}

	// Бэкап оригинала рядом с конфигом.
	bak := configPath + ".surifleet-bak-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.WriteFile(bak, raw, 0o644); err != nil {
		return fmt.Errorf("бэкап конфига: %w", err)
	}
	log.Info("бэкап suricata.yaml создан", "path", bak)

	if !managedPresent {
		entry := "  - " + managedRulesFile
		insertAt := secIdx + 1
		if lastItem >= 0 {
			insertAt = lastItem + 1
		}
		lines = append(lines[:insertAt], append([]string{entry}, lines[insertAt:]...)...)
	}
	return os.WriteFile(configPath, []byte(strings.Join(lines, "\n")), 0o644)
}

// --- файловые операции с бэкапом и атомарной записью ---

// backupFile копирует существующий файл рядом (.surifleet-bak-TS).
func backupFile(target string) (path string, ok bool, err error) {
	raw, err := os.ReadFile(target)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, err
	}
	path = target + ".surifleet-bak-" + time.Now().UTC().Format("20060102T150405Z")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", false, err
	}
	return path, true, nil
}

// writeFileAtomic — tmp-файл в том же каталоге + rename (атомарно в пределах FS).
func writeFileAtomic(target string, data []byte, perm os.FileMode) error {
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// rollback возвращает прежний managed-файл из бэкапа (или удаляет новый).
func rollback(target, backup string, hadBackup bool, log *slog.Logger) {
	if hadBackup {
		if err := os.Rename(backup, target); err != nil {
			log.Error("откат managed-файла", "err", err)
			return
		}
		log.Info("managed-файл откачен из бэкапа", "path", target)
		return
	}
	if err := os.Remove(target); err != nil {
		log.Error("удаление неудачного managed-файла", "err", err)
		return
	}
	log.Info("неудачный managed-файл удалён (бэкапа не было)", "path", target)
}

// --- валидация и управление движком ---

// validateConfig — suricata -T -c <config> (тест конфигурации и правил).
func validateConfig(configPath string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), validateTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "suricata", "-T", "-c", configPath)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// commandSocket — путь unix-command сокета из конфига инстанса
// (unix-command.filename), иначе дефолт.
func commandSocket(configPath string) string {
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return defaultCommandSocket
	}
	lines := strings.Split(string(raw), "\n")
	inSection := false
	for _, ln := range lines {
		trimmed := strings.TrimSpace(ln)
		if strings.HasPrefix(trimmed, "unix-command:") {
			inSection = true
			continue
		}
		if inSection {
			if strings.HasPrefix(trimmed, "filename:") {
				v := strings.TrimSpace(strings.TrimPrefix(trimmed, "filename:"))
				v = strings.Trim(v, `"'`)
				if v == "" {
					break
				}
				if !filepath.IsAbs(v) {
					v = filepath.Join("/var/run/suricata", v)
				}
				return v
			}
			// Конец секции — неиндентированная строка.
			if trimmed != "" && !strings.HasPrefix(ln, " ") && !strings.HasPrefix(ln, "\t") {
				break
			}
		}
	}
	return defaultCommandSocket
}

// suricatasc — вызов команды через unix-command сокет, возврат сырого вывода.
func suricatasc(socket, command string, timeout time.Duration) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "suricatasc", "-c", command, socket)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// scResponse — ответ suricatasc (последняя JSON-строка вывода).
type scResponse struct {
	Return  string          `json:"return"`
	Message json.RawMessage `json:"message"`
}

// parseSCResponse извлекает последний JSON-объект из вывода suricatasc
// (перед ним может быть баннер версии).
func parseSCResponse(out string) (*scResponse, error) {
	var last *scResponse
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.HasPrefix(ln, "{") {
			continue
		}
		var r scResponse
		if err := json.Unmarshal([]byte(ln), &r); err == nil {
			rr := r
			last = &rr
		}
	}
	if last == nil {
		return nil, fmt.Errorf("JSON-ответ не найден в выводе: %s", tail(out, 5))
	}
	return last, nil
}

// failedRule — правило, отклонённое движком (ruleset-failed-rules).
type failedRule struct {
	Sid   int64
	Error string
}

// failedRules — список не загрузившихся правил по ruleset-failed-rules.
// Формат message парсится защитно: массив объектов {sid, error} либо map.
func failedRules(socket string) ([]failedRule, error) {
	out, err := suricatasc(socket, "ruleset-failed-rules", suricatascTimeout)
	if err != nil {
		return nil, fmt.Errorf("suricatasc: %w; вывод: %s", err, tail(out, 5))
	}
	resp, err := parseSCResponse(out)
	if err != nil {
		return nil, err
	}
	if !strings.EqualFold(resp.Return, "OK") {
		return nil, fmt.Errorf("return=%s", resp.Return)
	}
	if len(resp.Message) == 0 || string(resp.Message) == "null" {
		return nil, nil
	}
	// Вариант 1: массив объектов.
	var arr []map[string]any
	if err := json.Unmarshal(resp.Message, &arr); err == nil {
		return scFailedFromList(arr), nil
	}
	// Вариант 2: map (sid → error или вложенные объекты).
	var m map[string]any
	if err := json.Unmarshal(resp.Message, &m); err == nil {
		list := make([]map[string]any, 0, len(m))
		for k, v := range m {
			if obj, ok := v.(map[string]any); ok {
				if _, hasSid := obj["sid"]; !hasSid {
					obj["sid"] = k
				}
				list = append(list, obj)
			} else {
				list = append(list, map[string]any{"sid": k, "error": fmt.Sprint(v)})
			}
		}
		return scFailedFromList(list), nil
	}
	return nil, fmt.Errorf("неизвестный формат message: %s", tail(string(resp.Message), 3))
}

// scFailedFromList — sid/error из списка объектов (защитный разбор типов).
func scFailedFromList(arr []map[string]any) []failedRule {
	out := make([]failedRule, 0, len(arr))
	for _, obj := range arr {
		var fr failedRule
		switch v := obj["sid"].(type) {
		case float64:
			fr.Sid = int64(v)
		case string:
			fmt.Sscanf(v, "%d", &fr.Sid)
		}
		switch v := obj["error"].(type) {
		case string:
			fr.Error = v
		default:
			fr.Error = fmt.Sprint(v)
		}
		if fr.Sid != 0 {
			out = append(out, fr)
		}
	}
	return out
}

// --- разбор ruleset-файла ---

var (
	sidRe = regexp.MustCompile(`\bsid:\s*(\d+)`)
	revRe = regexp.MustCompile(`\brev:\s*(\d+)`)
)

// parseSids — sid → rev всех правил файла (пропускает комментарии/пустые).
func parseSids(data []byte) map[int64]int32 {
	out := map[int64]int32{}
	for _, ln := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(ln)
		if len(trimmed) == 0 || trimmed[0] == '#' {
			continue
		}
		m := sidRe.FindSubmatch(trimmed)
		if m == nil {
			continue
		}
		var sid int64
		fmt.Sscanf(string(m[1]), "%d", &sid)
		var rev int32 = 1
		if rm := revRe.FindSubmatch(trimmed); rm != nil {
			fmt.Sscanf(string(rm[1]), "%d", &rev)
		}
		out[sid] = rev
	}
	return out
}

// tail — последние n строк вывода (для диагностики в ошибках).
func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
