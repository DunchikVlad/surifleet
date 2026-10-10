// log_rotation.go — ротация логов Suricata инстанса (чанк 111, backlog
// заказчика 2026-10-11; capability log_rotation): сервер шлёт
// LogRotationTask (report_only / rotate с порогом размера и числом архивов),
// агент в log_dir инстанса копирует содержимое активных лог-файлов в архив
// («имя.ГГГГММДД-ЧЧММСС») и усекает оригинал (copytruncate — дескрипторы
// Suricата остаются валидными, рестарт движка не нужен), старые архивы
// чистит (keep на базовое имя). Файлы меньше порога не трогаются — повторная
// ротация после усечения no-op по построению, журнал идемпотентности не
// пишется (как у service_action чанка 105).
package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// Пороги по умолчанию для ротации (нулевые поля задачи).
const (
	logRotateDefaultMinKB int64 = 64 // файлы меньше 64 КБ не ротируем
	logRotateDefaultKeep  int32 = 5  // архивов на базовое имя
)

// logRotateStamp — суффикс архива: «eve.json.20261011-133000».
const logRotateStamp = "20060102-150405"

// archiveSuffixRe — имя архива ротации: базовое имя + «.ГГГГММДД-ЧЧММСС»
// (12 цифр, дефис, 6 цифр). Файлы с таким суффиксом в отчёт не входят и
// сами не ротируются.
var archiveSuffixRe = regexp.MustCompile(`\.\d{8}-\d{6}$`)

// foreignArchiveRe — архивы ЧУЖОЙ ротации (системный logrotate на сенсоре:
// «eve.json.1.gz», «fast.log.2», «stats.log.3.xz») — исключаем из отчёта
// и не трогаем: копировать и усечать уже заархивированное бессмысленно и
// ломает нумерацию чужого ротатора. Живой e2e чанка 111 на стенде: в
// log_dir Suricата рядом с активными файлами лежат именно такие .N.gz.
var foreignArchiveRe = regexp.MustCompile(`\.\d+(\.(gz|xz|zst|bz2))?$`)

// isArchiveName — файл является архивом (нашей или чужой ротации).
func isArchiveName(name string) bool {
	return archiveSuffixRe.MatchString(name) || foreignArchiveRe.MatchString(name)
}

// executeLogRotation — отчёт или ротация логов инстанса (log_dir из
// привязки bound_instances).
func (e *taskExecutor) executeLogRotation(task *agentv1.Task, lr *agentv1.LogRotationTask) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID, "instance_id", lr.GetInstanceId(), "report_only", lr.GetReportOnly())

	if !e.hasCap("log_rotation") {
		e.failLogRotation(taskID, nil, "capability log_rotation не включена для хоста")
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.failLogRotation(taskID, nil, "дедлайн задачи истёк")
		return
	}
	b := e.bindings[lr.GetInstanceId()]
	if b == nil || b.GetLogDir() == "" {
		e.failLogRotation(taskID, nil, "инстанс не привязан к агенту (нет bound_instances)")
		return
	}
	minKB := lr.GetMinSizeKb()
	if minKB <= 0 {
		minKB = logRotateDefaultMinKB
	}
	keep := lr.GetKeep()
	if keep <= 0 {
		keep = logRotateDefaultKeep
	}

	res := &agentv1.LogRotationResult{InstanceId: lr.GetInstanceId()}
	rotated, err := rotateLogs(b.GetLogDir(), minKB, int(keep), lr.GetReportOnly(), res)
	if err != nil {
		e.failLogRotation(taskID, res, err.Error())
		return
	}
	log.Info("ротация логов завершена", "rotated", rotated, "freed_bytes", res.FreedBytes, "archived", len(res.Archived))
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: &agentv1.TaskResult_LogRotation{LogRotation: res},
	})
}

// rotateLogs — чистая функция ротации (для юнит-тестов): собирает файлы
// журнала log_dir (архивы исключаются), ротирует подходящие под порог,
// пишет итог в res. Возвращает число ротированных файлов.
func rotateLogs(dir string, minKB int64, keep int, reportOnly bool, res *agentv1.LogRotationResult) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, fmt.Errorf("чтение каталога логов %s: %w", dir, err)
	}
	var names []string
	for _, en := range entries {
		if en.IsDir() || isArchiveName(en.Name()) {
			continue
		}
		names = append(names, en.Name())
	}
	sort.Strings(names)

	var rotated int
	for _, name := range names {
		path := filepath.Join(dir, name)
		fi, err := os.Stat(path)
		if err != nil || !fi.Mode().IsRegular() {
			continue // симлинки/сокеты/прочее — не трогаем
		}
		info := &agentv1.LogFileInfo{Name: name, SizeBytes: fi.Size()}
		res.Files = append(res.Files, info)
		if fi.Size() < minKB*1024 {
			continue
		}
		info.Rotated = true
		if reportOnly {
			res.RotatedCount++ // в отчёте — число файлов, ПОДЛЕЖАЩИХ ротации
			continue
		}
		arch, err := copyTruncate(path, fi, time.Now())
		if err != nil {
			return rotated, fmt.Errorf("ротация %s: %w", name, err)
		}
		res.Archived = append(res.Archived, arch)
		res.FreedBytes += fi.Size()
		rotated++
		res.RotatedCount++
		if n, err := pruneArchives(dir, name, keep); err == nil {
			_ = n
		}
	}
	return rotated, nil
}

// copyTruncate — копирует содержимое файла в архив «имя.ГГГГММДД-ЧЧММСС»
// (права как у оригинала) и усекает оригинал до нуля. Атомарности между
// копией и усечением нет (как у logrotate copytruncate) — потеря строк,
// записанных в окно копирования, допустима по семантике copytruncate.
func copyTruncate(path string, fi fs.FileInfo, now time.Time) (string, error) {
	src, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer src.Close()
	arch := path + "." + now.Format(logRotateStamp)
	dst, err := os.OpenFile(arch, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fi.Mode().Perm())
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}
	if err := os.Truncate(path, 0); err != nil {
		return "", err
	}
	return filepath.Base(arch), nil
}

// pruneArchives — оставляет keep свежих архивов базового имени, остальные
// удаляет. Ошибки удаления не роняют задачу (каталог логов может не
// позволять — главное, что ротация прошла).
func pruneArchives(dir, base string, keep int) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var archs []string
	for _, en := range entries {
		if en.IsDir() || !strings.HasPrefix(en.Name(), base+".") || !archiveSuffixRe.MatchString(en.Name()) {
			continue
		}
		archs = append(archs, en.Name())
	}
	sort.Strings(archs) // суффикс лексикографически сортируется по времени
	var removed int
	for len(archs) > keep {
		if err := os.Remove(filepath.Join(dir, archs[0])); err != nil {
			return removed, err
		}
		archs = archs[1:]
		removed++
	}
	return removed, nil
}

// failLogRotation — failed с деталями LogRotationResult (без журнала:
// повтор после починки безопасен, как failSvc).
func (e *taskExecutor) failLogRotation(taskID string, res *agentv1.LogRotationResult, msg string) {
	e.log.Warn("ротация логов завершилась ошибкой", "task_id", taskID, "error", msg)
	var details *agentv1.TaskResult_LogRotation
	if res != nil {
		details = &agentv1.TaskResult_LogRotation{LogRotation: res}
	}
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_FAILED,
		Error:   msg,
		Details: details,
	})
}
