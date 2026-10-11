// update_config.go — конфиги suricata-update с хоста (чанк 113, backlog
// заказчика; capability config): fetch — чтение update.yaml / enable.conf /
// disable.conf / modify.conf / update/sources/<name>.yaml с сенсора
// (редактор «как на хосте» по образцу fetch_config), deploy — запись с
// бэкапом (.surifleet-bak-TS, ротация backupKeep чанка 109) и YAML-
// валидацией. Рестарт движка НЕ нужен — suricata-update подхватит файл при
// следующем прогоне. Оптимистичная конкурентность: expected_sha256 от
// последнего fetch. Файлов на сенсоре может не быть (suricata-update на
// дефолтах — стенд): fetch отдаёт exists=false, deploy создаёт файл.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// updateConfigMaxSize — предел содержимого конфига suricata-update (1 МБ;
// файлы по определению малы).
const updateConfigMaxSize = 1 << 20

// sysConfDir — sysconfdir suricata-update на сенсорах (дефолт сборки).
const sysConfDir = "/etc/suricata"

// updateConfigPath — канонический путь управляемого конфига. Для
// SOURCE_YAML имя валидируется (без путей/слешей — защита от выхода за
// пределы update/sources/).
func updateConfigPath(file agentv1.UpdateConfigFile, sourceName string) (string, error) {
	switch file {
	case agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_UPDATE_YAML:
		return filepath.Join(sysConfDir, "update.yaml"), nil
	case agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_ENABLE_CONF:
		return filepath.Join(sysConfDir, "enable.conf"), nil
	case agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_DISABLE_CONF:
		return filepath.Join(sysConfDir, "disable.conf"), nil
	case agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_MODIFY_CONF:
		return filepath.Join(sysConfDir, "modify.conf"), nil
	case agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML:
		name := strings.TrimSpace(sourceName)
		if name == "" || strings.ContainsAny(name, "/\\\x00") || name == "." || name == ".." {
			return "", fmt.Errorf("недопустимое имя источника: %q", sourceName)
		}
		return filepath.Join(sysConfDir, "update", "sources", name+".yaml"), nil
	default:
		return "", fmt.Errorf("неизвестный тип конфига: %s", file.String())
	}
}

// executeFetchUpdateConfig — чтение конфига suricata-update (read-only,
// журнал идемпотентности не пишется, как у fetch_config).
func (e *taskExecutor) executeFetchUpdateConfig(task *agentv1.Task, fc *agentv1.FetchUpdateConfigTask) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID, "file", fc.GetFile().String(), "source", fc.GetSourceName())

	if !e.hasCap("config") {
		e.failUpdateConfig(taskID, nil, "capability config не включена для хоста")
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.failUpdateConfig(taskID, nil, "дедлайн задачи истёк")
		return
	}
	path, err := updateConfigPath(fc.GetFile(), fc.GetSourceName())
	if err != nil {
		e.failUpdateConfig(taskID, nil, err.Error())
		return
	}
	res := &agentv1.FetchUpdateConfigResult{Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			// Файла нет — suricata-update на дефолтах; редактор создаст.
			log.Info("конфига suricata-update нет (дефолты)", "path", path)
			e.reply(&agentv1.TaskResult{
				TaskId:  taskID,
				Status:  agentv1.TaskStatus_TASK_STATUS_SUCCESS,
				Details: &agentv1.TaskResult_FetchUpdateConfig{FetchUpdateConfig: res},
			})
			return
		}
		e.failUpdateConfig(taskID, nil, "чтение конфига: "+err.Error())
		return
	}
	if len(data) > updateConfigMaxSize {
		e.failUpdateConfig(taskID, nil, fmt.Sprintf("конфиг больше предела %d байт", updateConfigMaxSize))
		return
	}
	sum := sha256.Sum256(data)
	res.Content = string(data)
	res.Sha256 = hex.EncodeToString(sum[:])
	res.SizeBytes = int64(len(data))
	res.Exists = true
	log.Info("конфиг suricata-update прочитан", "path", path, "bytes", len(data))
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: &agentv1.TaskResult_FetchUpdateConfig{FetchUpdateConfig: res},
	})
}

// executeDeployUpdateConfig — запись конфига suricata-update: журнал
// идемпотентности (по образцу deploy_config), expected_sha256 (оптимистичная
// конкурентность), unchanged (содержимое совпало — записи нет), бэкап →
// YAML-валидация → атомарная запись.
func (e *taskExecutor) executeDeployUpdateConfig(task *agentv1.Task, dc *agentv1.DeployUpdateConfigTask) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID, "file", dc.GetFile().String(), "source", dc.GetSourceName())

	if !e.hasCap("config") {
		e.failUpdateConfig(taskID, nil, "capability config не включена для хоста")
		return
	}
	if cached, ok := e.lookupProcessed(taskID); ok {
		log.Info("задача уже обработана — повторяю сохранённый результат", "status", cached.Status)
		e.reply(&agentv1.TaskResult{TaskId: taskID, Status: protoStatus(cached.Status), Error: cached.Error})
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.failUpdateConfig(taskID, nil, "дедлайн задачи истёк")
		return
	}
	path, err := updateConfigPath(dc.GetFile(), dc.GetSourceName())
	if err != nil {
		e.failUpdateConfig(taskID, nil, err.Error())
		return
	}
	data := []byte(dc.GetContent())
	if len(data) > updateConfigMaxSize {
		e.failUpdateConfig(taskID, nil, fmt.Sprintf("конфиг больше предела %d байт", updateConfigMaxSize))
		return
	}
	res := &agentv1.DeployUpdateConfigResult{Path: path, InstanceId: dc.GetInstanceId(), File: dc.GetFile(), SourceName: dc.GetSourceName()}

	// Текущее состояние: expected_sha / unchanged.
	current, rerr := os.ReadFile(path)
	exists := rerr == nil
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		e.failUpdateConfig(taskID, res, "чтение текущего конфига: "+rerr.Error())
		return
	}
	if exists {
		csum := sha256.Sum256(current)
		csha := hex.EncodeToString(csum[:])
		if exp := strings.ToLower(dc.GetExpectedSha256()); exp != "" && exp != csha {
			e.failUpdateConfig(taskID, res, "содержимое файла изменилось с момента загрузки (фактический sha "+csha[:12]+"…), перечитайте и повторите")
			return
		}
		if string(current) == string(data) {
			res.Sha256 = csha
			res.SizeBytes = int64(len(data))
			res.Unchanged = true
			e.replyUpdateConfigOK(taskID, res)
			return
		}
	} else if dc.GetExpectedSha256() != "" {
		// Fetch отдавал exists=false (sha пусто) — несоответствие ожиданий.
		e.failUpdateConfig(taskID, res, "файл появился с момента загрузки — перечитайте и повторите")
		return
	}

	// Валидация: YAML-парсинг для *.yaml; .conf — форматов нет, пропуск.
	if strings.HasSuffix(path, ".yaml") {
		var v any
		if err := yaml.Unmarshal(data, &v); err != nil {
			e.failUpdateConfig(taskID, res, "YAML не валиден: "+err.Error())
			return
		}
	}
	// Каталог выходного файла (update/sources может отсутствовать).
	if dir := filepath.Dir(path); dir != sysConfDir {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			e.failUpdateConfig(taskID, res, "создание каталога: "+err.Error())
			return
		}
	}
	backup := ""
	if exists {
		b, _, berr := backupFile(path)
		if berr != nil {
			e.failUpdateConfig(taskID, res, "бэкап конфига: "+berr.Error())
			return
		}
		backup = filepath.Base(b)
	}
	if err := writeFileAtomic(path, data, 0o644); err != nil {
		e.failUpdateConfig(taskID, res, "запись конфига: "+err.Error())
		return
	}
	sum := sha256.Sum256(data)
	res.Sha256 = hex.EncodeToString(sum[:])
	res.SizeBytes = int64(len(data))
	res.Backup = backup
	log.Info("конфиг suricata-update записан", "path", path, "bytes", len(data), "backup", backup)
	e.saveProcessed(taskID, cachedResult{Status: "succeeded"})
	e.replyUpdateConfigOK(taskID, res)
}

// replyUpdateConfigOK — succeeded с DeployUpdateConfigResult.
func (e *taskExecutor) replyUpdateConfigOK(taskID string, res *agentv1.DeployUpdateConfigResult) {
	e.reply(&agentv1.TaskResult{
		TaskId:  taskID,
		Status:  agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: &agentv1.TaskResult_DeployUpdateConfig{DeployUpdateConfig: res},
	})
}

// failUpdateConfig — failed для задач конфигов suricata-update (детали
// прилагаем, когда есть — сервер пишет историю применений).
func (e *taskExecutor) failUpdateConfig(taskID string, res any, msg string) {
	e.log.Warn("задача конфига suricata-update завершилась ошибкой", "task_id", taskID, "error", msg)
	reply := &agentv1.TaskResult{TaskId: taskID, Status: agentv1.TaskStatus_TASK_STATUS_FAILED, Error: msg}
	switch r := res.(type) {
	case *agentv1.DeployUpdateConfigResult:
		reply.Details = &agentv1.TaskResult_DeployUpdateConfig{DeployUpdateConfig: r}
	case *agentv1.FetchUpdateConfigResult:
		reply.Details = &agentv1.TaskResult_FetchUpdateConfig{FetchUpdateConfig: r}
	}
	e.reply(reply)
}
