package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestPruneOldBackups — ротация .surifleet-bak-* (чанк 109): остаются keep
// свежих по UTC-метке в имени, остальные удаляются.
func TestPruneOldBackups(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "zz-surifleet-managed.rules")
	if err := os.WriteFile(target, []byte("rules"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamps := []string{"20260101T000000Z", "20260102T000000Z", "20260103T000000Z",
		"20260104T000000Z", "20260105T000000Z"}
	for _, ts := range stamps {
		p := target + ".surifleet-bak-" + ts
		if err := os.WriteFile(p, []byte(ts), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Чужой файл с похожим именем трогать нельзя.
	other := filepath.Join(dir, "other.rules.surifleet-bak-20260101T000000Z")
	if err := os.WriteFile(other, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	pruneOldBackups(target, 2)

	left, err := filepath.Glob(target + ".surifleet-bak-*")
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(left)
	if len(left) != 2 {
		t.Fatalf("осталось бэкапов %d, ожидается 2: %v", len(left), left)
	}
	for i, ts := range []string{"20260104T000000Z", "20260105T000000Z"} {
		want := target + ".surifleet-bak-" + ts
		if left[i] != want {
			t.Errorf("left[%d] = %s, ожидается %s", i, left[i], want)
		}
	}
	if _, err := os.Stat(other); err != nil {
		t.Errorf("чужой бэкап удалён или недоступен: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Errorf("основной файл удалён или недоступен: %v", err)
	}
}

// TestPruneOldBackupsFew — бэкапов меньше лимита: ничего не удаляется.
func TestPruneOldBackupsFew(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "suricata.yaml")
	for _, ts := range []string{"20260101T000000Z", "20260102T000000Z"} {
		if err := os.WriteFile(target+".surifleet-bak-"+ts, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pruneOldBackups(target, 3)
	left, _ := filepath.Glob(target + ".surifleet-bak-*")
	if len(left) != 2 {
		t.Fatalf("осталось %d, ожидается 2 (удалять нечего)", len(left))
	}
}

// TestBackupFilePrunes — backupFile создаёт бэкап и подчищает старые.
func TestBackupFilePrunes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "f.rules")
	if err := os.WriteFile(target, []byte("v0"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < backupKeep+2; i++ {
		if _, ok, err := backupFile(target); err != nil || !ok {
			t.Fatalf("backupFile #%d: ok=%v err=%v", i, ok, err)
		}
		// Имена с секундной меткой — разносим искусственно.
		baks, _ := filepath.Glob(target + ".surifleet-bak-*")
		if len(baks) > 0 {
			fresh := baks[len(baks)-1]
			os.Rename(fresh, fresh+"x") // уникализируем, затем вернём суффикс
			os.Rename(fresh+"x", target+".surifleet-bak-2026010"+string(rune('0'+i))+"T000000Z")
		}
	}
	left, _ := filepath.Glob(target + ".surifleet-bak-*")
	if len(left) != backupKeep {
		t.Fatalf("после %d бэкапов осталось %d, ожидается %d", backupKeep+2, len(left), backupKeep)
	}
}
