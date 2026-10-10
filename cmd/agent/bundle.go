// bundle.go — сбор диагностического бандла одной кнопкой (чанк 106,
// п. 7 ТЗ «Реагирование: сбор диагностического бандла»): сервер шлёт
// CollectBundleTask с presigned PUT URL, агент собирает tar.gz (логи
// агента/Suricata, конфиги инстансов, системная информация) и загружает
// в S3. Результат — BundleResult {signed_url, size_bytes}.
//
// Задача чисто читающая (как fetch_config): журнал идемпотентности не
// пишется. Capability-гейт — monitoring (диагностика входит в консер-
// вативный дефолтный набор, п. 4 ТЗ): bundle не меняет состояние хоста.
// Каждый файл ограничен хвостом (bundleMaxFileBytes) — бандл остаётся
// компактным; отсутствующие файлы пропускаются с записью в манифест.
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

const (
	// bundleMaxFileBytes — предел на один файл в бандле (хвост, 1 МБ):
	// eve.json/suricata.log могут быть гигабайтами.
	bundleMaxFileBytes = 1 << 20
	// bundleMaxTotalBytes — защитный предел всего несжатого бандла (32 МБ).
	bundleMaxTotalBytes = 32 << 20
)

// bundleFile — один файл архива: имя в архиве + содержимое.
type bundleFile struct {
	name string
	data []byte
}

// executeCollectBundle — собрать диагностический бандл и загрузить на
// upload_url (presigned PUT).
func (e *taskExecutor) executeCollectBundle(task *agentv1.Task, cb *agentv1.CollectBundleTask) {
	taskID := task.GetTaskId()
	log := e.log.With("task_id", taskID)

	if !e.hasCap("monitoring") {
		e.failBundle(taskID, "capability monitoring не включена для хоста")
		return
	}
	if dl := task.GetDeadline(); dl != nil && time.Now().After(dl.AsTime()) {
		e.failBundle(taskID, "дедлайн задачи истёк")
		return
	}
	if cb.GetUploadUrl() == "" {
		e.failBundle(taskID, "upload_url не задан — некуда загружать бандл")
		return
	}

	var files []bundleFile
	var skipped []string
	add := func(name string, data []byte) {
		files = append(files, bundleFile{name: name, data: data})
	}
	addTail := func(name, path string) {
		data, err := readTail(path, bundleMaxFileBytes)
		if err != nil {
			skipped = append(skipped, fmt.Sprintf("%s (%s): %v", name, path, err))
			return
		}
		add(name, data)
	}

	// Логи агента (чанк 5: lumberjack пишет в data_dir/agent.log или
	// cfg.LogFile — executor знает dataDir; ротированные файлы не тащим).
	if cb.GetIncludeAgentLogs() {
		addTail("agent/agent.log", filepath.Join(e.dataDir, "agent.log"))
	}
	// Логи и конфиги инстансов — по привязкам HelloAck.
	for _, b := range e.bindings {
		safe := sanitizeName(b.GetName())
		if cb.GetIncludeSuricataLogs() && b.GetLogDir() != "" {
			addTail(safe+"/suricata.log", filepath.Join(b.GetLogDir(), "suricata.log"))
			addTail(safe+"/eve.json", filepath.Join(b.GetLogDir(), "eve.json"))
		}
		if cb.GetIncludeConfigs() && b.GetConfigPath() != "" {
			addTail(safe+"/suricata.yaml", b.GetConfigPath())
		}
	}
	// Системная информация.
	if cb.GetIncludeSystemInfo() {
		add("sysinfo.txt", e.collectSysInfo())
	}

	// Манифест всегда — что вошло и что пропущено.
	var manifest strings.Builder
	manifest.WriteString("surifleet diagnostic bundle\n")
	manifest.WriteString("собран: " + time.Now().UTC().Format(time.RFC3339) + "\n")
	for _, f := range files {
		fmt.Fprintf(&manifest, "включён: %s (%d байт)\n", f.name, len(f.data))
	}
	for _, s := range skipped {
		manifest.WriteString("пропущен: " + s + "\n")
	}
	add("manifest.txt", []byte(manifest.String()))

	data, err := packTarGz(files)
	if err != nil {
		e.failBundle(taskID, "сборка архива: "+err.Error())
		return
	}
	if err := uploadFile(cb.GetUploadUrl(), data); err != nil {
		e.failBundle(taskID, "загрузка бандла: "+err.Error())
		return
	}
	log.Info("диагностический бандл загружен", "bytes", len(data), "files", len(files), "skipped", len(skipped))
	e.reply(&agentv1.TaskResult{
		TaskId: taskID,
		Status: agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		Details: &agentv1.TaskResult_Bundle{Bundle: &agentv1.BundleResult{
			SignedUrl: cb.GetUploadUrl(),
			SizeBytes: int64(len(data)),
		}},
	})
}

// collectSysInfo — текстовая сводка о хосте: ОС, uptime, память, диск,
// состояние systemd-юнитов инстансов, версии агента и Suricata.
func (e *taskExecutor) collectSysInfo() []byte {
	var b strings.Builder
	b.WriteString("== версия агента ==\n" + version + "\n\n")
	run := func(title string, name string, args ...string) {
		b.WriteString("== " + title + " ==\n")
		out, err := exec.Command(name, args...).CombinedOutput()
		if err != nil {
			fmt.Fprintf(&b, "(ошибка: %v)\n", err)
		}
		b.Write(out)
		b.WriteString("\n\n")
	}
	run("uname", "uname", "-a")
	run("uptime", "uptime")
	run("free -m", "free", "-m")
	run("df -h", "df", "-h")
	if rep := e.disc.Load(); rep != nil {
		if bin := rep.GetBinary(); bin != nil {
			b.WriteString("== suricata ==\n" + bin.GetVersion() + "\n\n")
		}
		for _, in := range rep.GetInstances() {
			if u := in.GetSystemdUnit(); u != "" {
				run("systemctl status "+u, "systemctl", "status", u, "--no-pager", "-l")
			}
		}
	}
	return []byte(b.String())
}

// readTail — хвост файла размером до max байт (читаем с конца; бандл —
// про свежую диагностику, не про архив).
func readTail(path string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := fi.Size()
	off := int64(0)
	if size > maxBytes {
		off = size - maxBytes
	}
	buf := make([]byte, size-off)
	if _, err := f.ReadAt(buf, off); err != nil {
		return nil, err
	}
	return buf, nil
}

// packTarGz — tar.gz из набора файлов в памяти (с защитным пределом).
func packTarGz(files []bundleFile) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	var total int64
	for _, f := range files {
		total += int64(len(f.data))
		if total > bundleMaxTotalBytes {
			return nil, fmt.Errorf("бандл больше предела %d байт", bundleMaxTotalBytes)
		}
		if err := tw.WriteHeader(&tar.Header{
			Name:    f.name,
			Mode:    0o644,
			Size:    int64(len(f.data)),
			ModTime: time.Now(),
		}); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// sanitizeName — имя инстанса в безопасный компонент пути архива.
func sanitizeName(s string) string {
	s = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, s)
	if s == "" {
		return "instance"
	}
	return s
}

// failBundle — failed для collect_bundle (единая точка, как replySvc).
func (e *taskExecutor) failBundle(taskID, msg string) {
	e.log.Warn("задача завершилась ошибкой", "task_id", taskID, "error", msg)
	e.reply(&agentv1.TaskResult{
		TaskId: taskID,
		Status: agentv1.TaskStatus_TASK_STATUS_FAILED,
		Error:  msg,
	})
}
