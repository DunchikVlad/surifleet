package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// TestRotateLogsReportOnly — отчёт: файлы перечислены, порог помечен, ничего
// не записано и не ротировано.
func TestRotateLogsReportOnly(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("x", 100*1024) // 100 КБ > порога 64 КБ
	mustWrite(t, filepath.Join(dir, "eve.json"), []byte(big))
	mustWrite(t, filepath.Join(dir, "fast.log"), []byte("tiny\n")) // < порога

	res := &agentv1.LogRotationResult{}
	rotated, err := rotateLogs(dir, 64, 5, true, res)
	if err != nil {
		t.Fatalf("rotateLogs: %v", err)
	}
	if rotated != 0 {
		t.Fatalf("report_only: rotated=%d, ожидалось 0", rotated)
	}
	if res.RotatedCount != 1 {
		t.Fatalf("report: rotated_count=%d, ожидалось 1 (только eve.json под порогом)", res.RotatedCount)
	}
	if len(res.Files) != 2 || res.FreedBytes != 0 || len(res.Archived) != 0 {
		t.Fatalf("report: files=%d freed=%d archived=%d — ничего не должно меняться", len(res.Files), res.FreedBytes, len(res.Archived))
	}
	for _, f := range res.Files {
		if f.Name == "eve.json" && !f.Rotated {
			t.Fatalf("eve.json (100 КБ) должен быть помечен rotated")
		}
		if f.Name == "fast.log" && f.Rotated {
			t.Fatalf("fast.log (малый) не должен быть помечен rotated")
		}
	}
	// Файлы на месте и нетронуты.
	if fi, _ := os.Stat(filepath.Join(dir, "eve.json")); fi.Size() != int64(len(big)) {
		t.Fatalf("report_only изменил eve.json")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 2 {
		t.Fatalf("report_only создал файлы: %d", len(entries))
	}
}

// TestRotateLogsRotateAndPrune — ротация: большой файл архивируется и
// усекается, архивов хранится keep, архивы не входят в отчёт и сами не
// ротируются, повторная ротация — no-op.
func TestRotateLogsRotateAndPrune(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("y", 80*1024)
	mustWrite(t, filepath.Join(dir, "eve.json"), []byte(big))

	res := &agentv1.LogRotationResult{}
	rotated, err := rotateLogs(dir, 64, 2, false, res)
	if err != nil {
		t.Fatalf("rotateLogs: %v", err)
	}
	if rotated != 1 || res.RotatedCount != 1 {
		t.Fatalf("rotated=%d count=%d, ожидалось 1/1", rotated, res.RotatedCount)
	}
	if res.FreedBytes != int64(len(big)) {
		t.Fatalf("freed=%d, ожидалось %d", res.FreedBytes, len(big))
	}
	if len(res.Archived) != 1 || !strings.HasPrefix(res.Archived[0], "eve.json.") {
		t.Fatalf("archived=%v, ожидалось имя архива eve.json.*", res.Archived)
	}
	// Оригинал усёкся, архив содержит содержимое.
	if fi, _ := os.Stat(filepath.Join(dir, "eve.json")); fi.Size() != 0 {
		t.Fatalf("оригинал не усёкся: %d байт", fi.Size())
	}
	archData, err := os.ReadFile(filepath.Join(dir, res.Archived[0]))
	if err != nil || len(archData) != len(big) {
		t.Fatalf("архив: %v, %d байт, ожидалось %d", err, len(archData), len(big))
	}
	// Права архива совпадают с оригиналом.
	if m := fileMode(t, filepath.Join(dir, res.Archived[0])); m != fileMode(t, filepath.Join(dir, "eve.json")) {
		t.Fatalf("права архива %v != оригинала %v", m, fileMode(t, filepath.Join(dir, "eve.json")))
	}

	// Повторная ротация сразу после усечения — no-op (порог 1 КБ, файл пуст).
	res2 := &agentv1.LogRotationResult{}
	rotated2, err := rotateLogs(dir, 1, 2, false, res2)
	if err != nil {
		t.Fatalf("повторный rotateLogs: %v", err)
	}
	if rotated2 != 0 || res2.RotatedCount != 0 {
		t.Fatalf("повторная ротация после усечения не no-op: rotated=%d", rotated2)
	}
}

// TestPruneArchives — архивов на имя хранится ровно keep свежих.
func TestPruneArchives(t *testing.T) {
	dir := t.TempDir()
	big := strings.Repeat("y", 4*1024)
	base := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 4; i++ {
		mustWrite(t, filepath.Join(dir, "eve.json"), []byte(big))
		if _, err := copyTruncate(filepath.Join(dir, "eve.json"), fileInfoOf(t, dir, "eve.json"), base.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatalf("copyTruncate %d: %v", i, err)
		}
	}
	if _, err := pruneArchives(dir, "eve.json", 2); err != nil {
		t.Fatalf("pruneArchives: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	var archs []string
	for _, en := range entries {
		if archiveSuffixRe.MatchString(en.Name()) {
			archs = append(archs, en.Name())
		}
	}
	if len(archs) != 2 {
		t.Fatalf("архивов %d (%v), keep=2 — ожидалось 2", len(archs), archs)
	}
	// Остались именно самые свежие метки (ReadDir сортирует по имени возрастанием).
	if archs[0] >= archs[1] {
		t.Fatalf("архивы не по возрастанию: %v", archs)
	}
	last := base.Add(3 * time.Hour).Format(logRotateStamp)
	prev := base.Add(2 * time.Hour).Format(logRotateStamp)
	got := map[string]bool{archs[0]: true, archs[1]: true}
	if !got["eve.json."+last] || !got["eve.json."+prev] {
		t.Fatalf("хранятся не свежие архивы: %v (ожидались суффиксы %s, %s)", archs, last, prev)
	}
}

// TestRotateLogsSkipsArchivesAndDirs — архивы (наши .ГГГГММДД-ЧЧММСС и
// чужой системный logrotate .N[.gz]) и подкаталоги в отчёт не входят и
// не ротируются.
func TestRotateLogsSkipsArchivesAndDirs(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "eve.json"), []byte(strings.Repeat("z", 70*1024)))
	mustWrite(t, filepath.Join(dir, "eve.json.20261001-120000"), []byte("наш старый архив"))
	mustWrite(t, filepath.Join(dir, "eve.json.1.gz"), []byte("чужой архив logrotate"))
	mustWrite(t, filepath.Join(dir, "stats.log.2"), []byte("чужой нумерованный"))
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	res := &agentv1.LogRotationResult{}
	if _, err := rotateLogs(dir, 64, 5, true, res); err != nil {
		t.Fatalf("rotateLogs: %v", err)
	}
	if len(res.Files) != 1 || res.Files[0].Name != "eve.json" {
		names := []string{}
		for _, f := range res.Files {
			names = append(names, f.Name)
		}
		t.Fatalf("в отчёте %v, ожидался только eve.json", names)
	}
	// Чужие архивы на диске нетронуты (rotate не создаёт и не усекает их).
	if _, err := os.Stat(filepath.Join(dir, "eve.json.1.gz")); err != nil {
		t.Fatalf("чужой архив пострадал: %v", err)
	}
}

// TestRotateLogsMissingDir — понятная ошибка при отсутствии каталога.
func TestRotateLogsMissingDir(t *testing.T) {
	_, err := rotateLogs(filepath.Join(t.TempDir(), "нет-такого"), 64, 5, true, &agentv1.LogRotationResult{})
	if err == nil || !strings.Contains(err.Error(), "чтение каталога логов") {
		t.Fatalf("ожидалась ошибка чтения каталога, получено: %v", err)
	}
}

// TestPackageCmd — маппинг действий → apt/dpkg; мусор → отказ.
func TestPackageCmd(t *testing.T) {
	cases := []struct {
		action agentv1.PackageAction
		verb   string
		args   string
	}{
		{agentv1.PackageAction_PACKAGE_ACTION_CHECK, "dpkg-query", "-W -f=${Status} ${Version} suricata"},
		{agentv1.PackageAction_PACKAGE_ACTION_INSTALL, "apt-get", "install -y suricata"},
		{agentv1.PackageAction_PACKAGE_ACTION_REMOVE, "apt-get", "remove -y suricata"},
		{agentv1.PackageAction_PACKAGE_ACTION_UPDATE, "apt-get", "install --only-upgrade -y suricata"},
	}
	for _, c := range cases {
		verb, args, ok := packageCmd(c.action, "suricata")
		if !ok || verb != c.verb || strings.Join(args, " ") != c.args {
			t.Fatalf("%v: got %s %v %v, ожидалось %s %s", c.action, verb, args, ok, c.verb, c.args)
		}
	}
	if _, _, ok := packageCmd(agentv1.PackageAction_PACKAGE_ACTION_UNSPECIFIED, "suricata"); ok {
		t.Fatalf("UNSPECIFIED не должен давать команду")
	}
}

// TestPackageWhitelist — управляем только suricata/suricata-update.
func TestPackageWhitelist(t *testing.T) {
	if !pkgWhitelist["suricata"] || !pkgWhitelist["suricata-update"] {
		t.Fatalf("белый список должен включать suricata и suricata-update")
	}
	if pkgWhitelist["nginx"] || pkgWhitelist["sudo"] {
		t.Fatalf("белый список не должен включать посторонние пакеты")
	}
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func fileMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func fileInfoOf(t *testing.T, dir, name string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return fi
}
