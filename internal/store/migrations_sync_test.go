package store

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMigrationsInSync гарантирует, что встроенные (embed) миграции совпадают
// с источником истины db/migrations: embed работает только внутри пакета,
// поэтому файлы дублируются и этот тест страхует от рассинхрона.
func TestMigrationsInSync(t *testing.T) {
	srcDir := filepath.Join("..", "..", "db", "migrations")
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("чтение embed-каталога: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("нет встроенных миграций")
	}
	for _, e := range entries {
		embedded, err := migrationsFS.ReadFile("migrations/" + e.Name())
		if err != nil {
			t.Fatalf("чтение встроенного файла %s: %v", e.Name(), err)
		}
		original, err := os.ReadFile(filepath.Join(srcDir, e.Name()))
		if err != nil {
			t.Fatalf("чтение db/migrations/%s: %v", e.Name(), err)
		}
		if string(embedded) != string(original) {
			t.Errorf("рассинхрон: internal/store/migrations/%s != db/migrations/%s — скопируйте файл заново", e.Name(), e.Name())
		}
	}
}
