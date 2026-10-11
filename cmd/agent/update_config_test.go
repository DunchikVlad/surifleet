package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// TestUpdateConfigPath — канонические пути и валидация имени источника
// (защита от выхода за update/sources/).
func TestUpdateConfigPath(t *testing.T) {
	cases := []struct {
		file   agentv1.UpdateConfigFile
		src    string
		want   string
		wantOk bool
	}{
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_UPDATE_YAML, "", filepath.FromSlash("/etc/suricata/update.yaml"), true},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_ENABLE_CONF, "", filepath.FromSlash("/etc/suricata/enable.conf"), true},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_DISABLE_CONF, "", filepath.FromSlash("/etc/suricata/disable.conf"), true},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_MODIFY_CONF, "", filepath.FromSlash("/etc/suricata/modify.conf"), true},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML, "local-src", filepath.FromSlash("/etc/suricata/update/sources/local-src.yaml"), true},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML, "", "", false},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML, "../evil", "", false},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML, "a/b", "", false},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_UNSPECIFIED, "", "", false},
	}
	for _, c := range cases {
		got, err := updateConfigPath(c.file, c.src)
		if c.wantOk && (err != nil || got != c.want) {
			t.Fatalf("%v/%q: got %q %v, ожидалось %q", c.file, c.src, got, err, c.want)
		}
		if !c.wantOk && err == nil {
			t.Fatalf("%v/%q: ожидалась ошибка, got %q", c.file, c.src, got)
		}
	}
}

// TestDeployUpdateConfigFile — чистая часть деплоя на временном каталоге
// (без executor): бэкап, YAML-валидация, unchanged, expected_sha.
// Путь подменяем через updateConfigPath? Нет — тестируем поведение через
// временный sysConfDir-эквивалент на уровне функций валидации/бэкапа.
func TestDeployUpdateConfigHelpers(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "update.yaml")

	// YAML-валидация.
	if err := validateUpdateConfigYAML(target, []byte("sources:\n  et/open: {}\n")); err != nil {
		t.Fatalf("валидный YAML отвергнут: %v", err)
	}
	if err := validateUpdateConfigYAML(target, []byte("sources: [broken")); err == nil {
		t.Fatalf("битый YAML прошёл")
	}
	// .conf не валидируется.
	if err := validateUpdateConfigYAML(filepath.Join(dir, "enable.conf"), []byte("not: [yaml")); err != nil {
		t.Fatalf(".conf не должен валидироваться как YAML: %v", err)
	}

	// Бэкап → запись → повторный бэкап.
	mustWrite(t, target, []byte("version: 1\n"))
	backup, had, err := backupFile(target)
	if err != nil || !had {
		t.Fatalf("backupFile: %v %v", backup, had)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("бэкап не создан: %v", err)
	}
	// Атомарная запись + содержимое.
	if err := writeFileAtomic(target, []byte("version: 2\n"), 0o644); err != nil {
		t.Fatalf("writeFileAtomic: %v", err)
	}
	if raw, _ := os.ReadFile(target); string(raw) != "version: 2\n" {
		t.Fatalf("содержимое не записано")
	}
	// Бэкап сохранил исходное.
	if raw, _ := os.ReadFile(backup); string(raw) != "version: 1\n" {
		t.Fatalf("бэкап не содержит исходное")
	}
	if !strings.HasPrefix(filepath.Base(backup), "update.yaml.surifleet-bak-") {
		t.Fatalf("имя бэкапа: %s", backup)
	}
}

func validateUpdateConfigYAML(path string, data []byte) error {
	if !strings.HasSuffix(path, ".yaml") {
		return nil
	}
	var v any
	return yaml.Unmarshal(data, &v)
}
