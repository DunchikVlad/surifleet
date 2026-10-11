package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeProc — каталог вида /proc с процессом cmdline.
func fakeProc(t *testing.T, procs map[int]string) string {
	t.Helper()
	dir := t.TempDir()
	for pid, cmdline := range procs {
		p := filepath.Join(dir, strconvItoa(pid))
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "cmdline"), []byte(cmdline), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func strconvItoa(i int) string {
	return string(rune('0'+i)) // тесты используют малые PID (1-9)
}

// TestFindSuricataUpdateProc — настоящий процесс (argv-элемент
// /usr/bin/suricata-update) находится; shell-враппер с подстрокой в
// едином argv не матчится; selfPid исключается.
func TestFindSuricataUpdateProc(t *testing.T) {
	dir := fakeProc(t, map[int]string{
		1: "python3\x00/usr/bin/suricata-update\x00update\x00",
		2: "bash -c 'pgrep -af suricata-update || echo empty'\x00", // враппер: подстрока, но не argv
		3: "systemd\x00",
	})
	pid, ok := findSuricataUpdateProc(dir, 99)
	if !ok || pid != 1 {
		t.Fatalf("got %d %v, ожидался pid 1", pid, ok)
	}
	// selfPid исключается.
	if _, ok := findSuricataUpdateProc(fakeProc(t, map[int]string{5: "python3\x00/usr/bin/suricata-update\x00"}), 5); ok {
		t.Fatalf("selfPid не должен матчиться")
	}
	// Нет процесса — нет нахождения.
	if _, ok := findSuricataUpdateProc(fakeProc(t, map[int]string{6: "suricata\x00-c\x00/etc/suricata/suricata.yaml\x00"}), 99); ok {
		t.Fatalf("suricata (движок) не должен матчиться под suricata-update")
	}
}

// TestIndexInfo — version парсится из index.yaml, время — mtime файла.
func TestIndexInfo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "index.yaml")
	if err := os.WriteFile(path, []byte("# This is a version 1 formatted index.\nversion: 1\n\nsources:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	if err := os.Chtimes(path, want, want); err != nil {
		t.Fatal(err)
	}
	ver, at, ok := indexInfo(path)
	if !ok || ver != "1" || !at.Equal(want) {
		t.Fatalf("got %q %v %v, ожидалось «1» и mtime", ver, at, ok)
	}
	// Нет файла — нейтрально.
	if _, _, ok := indexInfo(filepath.Join(dir, "нет.yaml")); ok {
		t.Fatalf("отсутствующий index.yaml не должен давать ok")
	}
	// Файл без version — ok=true (mtime живой), version пусто.
	other := filepath.Join(dir, "other.yaml")
	if err := os.WriteFile(other, []byte("sources:\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ver, _, ok := indexInfo(other); ok && ver != "" {
		t.Fatalf("version у файла без version: %q", ver)
	}
}

// TestSaveLoadUpdateStatus — round-trip трекинга прогона.
func TestSaveLoadUpdateStatus(t *testing.T) {
	dir := t.TempDir()
	if _, err := loadUpdateStatus(dir); err == nil {
		t.Fatalf("до первой записи должна быть ошибка чтения")
	}
	want := savedUpdateStatus{Result: "success", FinishedAt: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)}
	if err := saveUpdateStatus(dir, want); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := loadUpdateStatus(dir)
	if err != nil || got.Result != want.Result || got.FinishedAt != want.FinishedAt {
		t.Fatalf("round-trip: %+v (%v)", got, err)
	}
	// collectUpdateStatus подхватывает сохранённый прогон (proc пуст).
	st := collectUpdateStatus(fakeProc(t, map[int]string{}), dir, 99)
	if st.GetLastResult() != "success" || st.GetLastRunAt() == nil || st.GetRunning() {
		t.Fatalf("collect: %+v", st)
	}
}
