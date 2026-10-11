// update_status.go — статус suricata-update на хосте для heartbeat (чанк
// 112, backlog заказчика): идёт ли обновление прямо сейчас (процесс),
// итог/время последнего прогона (агент трекает свои запуски задачи
// suricata_update — пишет data_dir/suricata-update-status.json), версия и
// время индекса источников (update/cache/index.yaml). Сервер кладёт это в
// Redis (agent_upd:{agent_id}) и отдаёт API GET
// /instances/{id}/suricata_update/status.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// updateStatusFile — трекинг последнего прогона suricata-update агентом.
const updateStatusFile = "suricata-update-status.json"

// indexVersionRe — «version: N» из index.yaml (первая значимая строка).
var indexVersionRe = regexp.MustCompile(`^version:\s*(\S+)`)

// savedUpdateStatus — содержимое updateStatusFile.
type savedUpdateStatus struct {
	Result     string `json:"result"` // success | failed
	FinishedAt string `json:"finished_at"`
	Error      string `json:"error,omitempty"`
}

// collectUpdateStatus — сборка статуса для heartbeat. Все шаги
// best-effort: любая недоступность даёт нейтральное значение, heartbeat
// не должен падать из-за диагностики.
func collectUpdateStatus(procDir, dataDir string, selfPid int) *agentv1.SuricataUpdateStatus {
	st := &agentv1.SuricataUpdateStatus{}
	if pid, ok := findSuricataUpdateProc(procDir, selfPid); ok {
		st.Running = true
		st.Pid = pid
	}
	if s, err := loadUpdateStatus(dataDir); err == nil && s.Result != "" {
		st.LastResult = s.Result
		if t, err := time.Parse(time.RFC3339, s.FinishedAt); err == nil {
			st.LastRunAt = timestamppb.New(t)
		}
		st.LastError = s.Error
	}
	if ver, at, ok := indexInfo(indexPath); ok {
		st.IndexVersion = ver
		st.IndexUpdatedAt = timestamppb.New(at)
	}
	return st
}

// findSuricataUpdateProc — PID процесса suricata-update (скан /proc по
// argv: basename любого аргумента == «suricata-update»). Надёжнее pgrep
// -f: shell-врапперы, чья командная строка содержит подстроку
// «suricata-update» (наши же ssh/sudo обёртки), не матчатся — argv у них
// единым аргументом «bash -c …». selfPid исключается.
func findSuricataUpdateProc(procDir string, selfPid int) (int64, bool) {
	entries, err := os.ReadDir(procDir)
	if err != nil {
		return 0, false
	}
	for _, en := range entries {
		pid, err := strconv.Atoi(en.Name())
		if err != nil || pid == selfPid {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(procDir, en.Name(), "cmdline"))
		if err != nil || len(raw) == 0 {
			continue
		}
		for _, arg := range strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00") {
			if filepath.Base(arg) == "suricata-update" {
				return int64(pid), true
			}
		}
	}
	return 0, false
}

// loadUpdateStatus — чтение трекинга последнего прогона (нет файла —
// норма: ещё не запускали).
func loadUpdateStatus(dataDir string) (*savedUpdateStatus, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, updateStatusFile))
	if err != nil {
		return nil, err
	}
	var s savedUpdateStatus
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// saveUpdateStatus — атомарная запись трекинга прогона (вызов из
// executeSuricataUpdate по факту завершения задачи с реальным update).
func saveUpdateStatus(dataDir string, s savedUpdateStatus) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := filepath.Join(dataDir, updateStatusFile+".tmp")
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dataDir, updateStatusFile))
}

// indexInfo — version из index.yaml + mtime файла (ок=false — индекса нет
// или не читается; свежему сенсору до первого update-sources индекс не
// нужен — нейтрально).
func indexInfo(path string) (string, time.Time, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, false
	}
	ver := ""
	for _, ln := range strings.Split(string(raw), "\n") {
		if m := indexVersionRe.FindStringSubmatch(strings.TrimSpace(ln)); m != nil {
			ver = m[1]
			break
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ver, time.Time{}, ver != ""
	}
	return ver, fi.ModTime(), true
}
