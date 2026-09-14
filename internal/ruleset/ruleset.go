// Package ruleset — сборка версий ruleset: детерминированный рендер
// выбранных правил в один .rules-текст и content-addressed хэш (sha256).
// Детерминизм — свойство безопасности: одинаковый состав правил всегда
// даёт одинаковый блоб и хэш (ключ блоба в S3, сверка desired/actual).
package ruleset

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// Render сливает сырые тексты правил в один .rules-документ:
// сортировка по sid (детерминизм независимо от порядка выборки),
// одна строка на правило, завершающий \n. Пустые строки отбрасываются.
func Render(rules []RawRule) []byte {
	sorted := make([]RawRule, len(rules))
	copy(sorted, rules)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SID < sorted[j].SID })
	var b strings.Builder
	for _, r := range sorted {
		line := strings.TrimSpace(r.Raw)
		if line == "" {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return []byte(b.String())
}

// RawRule — сырое правило для рендера (sid нужен для сортировки).
type RawRule struct {
	SID int64
	Raw string
}

// SHA256 — hex-хэш блоба (идентификатор ruleset и ключа в S3).
func SHA256(blob []byte) string {
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

// BlobKey — ключ блоба в бакете: rulesets/{sha256}.rules.
func BlobKey(sha256Hex string) string { return "rulesets/" + sha256Hex + ".rules" }
