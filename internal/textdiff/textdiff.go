// Package textdiff — построчный unified diff двух текстов (чанк 82,
// спека /config_profiles/{id}/versions/diff → DiffResult). Своя
// реализация на LCS (динамическое программирование) — без новых
// зависимостей; для yaml-конфигов (сотни–тысячи строк) достаточно.
package textdiff

import (
	"fmt"
	"strings"
)

// maxCells — предел таблицы LCS (len(a)×len(b)); сверх него дифф
// деградирует до «всё удалено / всё добавлено» — корректно, но без
// совпадающего контекста (страховка от OOM на гигантских входах).
const maxCells = 4_000_000

// Unified возвращает unified diff между a и b (contextLines строк
// контекста до/после изменений; fromName/toName — метки заголовков
// ---/+++). Пустая строка — тексты идентичны.
func Unified(a, b, fromName, toName string, contextLines int) string {
	if a == b {
		return ""
	}
	la, lb := splitLines(a), splitLines(b)
	ops := diffOps(la, lb)

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n+++ %s\n", fromName, toName)

	n := len(ops)
	i := 0
	for i < n {
		// Ближайшее изменение от i.
		j := i
		for j < n && ops[j].kind == opEqual {
			j++
		}
		if j == n {
			break // изменений больше нет
		}
		// Начало hunks — не раньше j-contextLines.
		start := j - contextLines
		if start < 0 {
			start = 0
		}
		// Конец hunks: после последнего изменения + contextLines; соседние
		// изменения, разделённые ≤ 2*contextLines совпадающих строк,
		// сливаются в один hunk.
		k := j
		lastChange := j
		for k < n {
			if ops[k].kind != opEqual {
				lastChange = k
				k++
				continue
			}
			run := 0
			for k+run < n && ops[k+run].kind == opEqual {
				run++
			}
			if run > 2*contextLines {
				break
			}
			k += run
		}
		end := lastChange + contextLines + 1
		if end > n {
			end = n
		}
		writeHunk(&sb, ops[start:end])
		i = end
	}
	return sb.String()
}

type opKind byte

const (
	opEqual opKind = iota
	opDel
	opAdd
)

// diffOp — одна строка диффа с позициями в исходном (oldPos) и новом
// (newPos) тексте (1-based; 0 — строка на той стороне отсутствует).
type diffOp struct {
	kind           opKind
	line           string
	oldPos, newPos int
}

// diffOps — последовательность операций превращения a в b (LCS).
func diffOps(a, b []string) []diffOp {
	var ops []diffOp
	if len(a)*len(b) <= maxCells {
		// dp[i][j] — длина LCS суффиксов a[i:], b[j:].
		dp := make([][]int, len(a)+1)
		for i := range dp {
			dp[i] = make([]int, len(b)+1)
		}
		for i := len(a) - 1; i >= 0; i-- {
			for j := len(b) - 1; j >= 0; j-- {
				switch {
				case a[i] == b[j]:
					dp[i][j] = dp[i+1][j+1] + 1
				case dp[i+1][j] >= dp[i][j+1]:
					dp[i][j] = dp[i+1][j]
				default:
					dp[i][j] = dp[i][j+1]
				}
			}
		}
		i, j := 0, 0
		for i < len(a) && j < len(b) {
			switch {
			case a[i] == b[j]:
				ops = append(ops, diffOp{opEqual, a[i], i + 1, j + 1})
				i++
				j++
			case dp[i+1][j] >= dp[i][j+1]:
				ops = append(ops, diffOp{opDel, a[i], i + 1, 0})
				i++
			default:
				ops = append(ops, diffOp{opAdd, b[j], 0, j + 1})
				j++
			}
		}
		for ; i < len(a); i++ {
			ops = append(ops, diffOp{opDel, a[i], i + 1, 0})
		}
		for ; j < len(b); j++ {
			ops = append(ops, diffOp{opAdd, b[j], 0, j + 1})
		}
		return ops
	}
	// Деградация для гигантских входов: всё удалить, всё добавить.
	for i, l := range a {
		ops = append(ops, diffOp{opDel, l, i + 1, 0})
	}
	for j, l := range b {
		ops = append(ops, diffOp{opAdd, l, 0, j + 1})
	}
	return ops
}

// writeHunk пишет один hunk в формате unified diff (@@ -x,y +u,v @@).
func writeHunk(sb *strings.Builder, ops []diffOp) {
	oldStart, newStart, oldCount, newCount := 0, 0, 0, 0
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			if oldStart == 0 {
				oldStart, newStart = op.oldPos, op.newPos
			}
			oldCount++
			newCount++
		case opDel:
			if oldStart == 0 {
				oldStart = op.oldPos
			}
			oldCount++
		case opAdd:
			if newStart == 0 {
				// hunk из одних добавлений: позиция новой стороны — первая
				// добавленная; старая сторона пуста (start = oldPos пред.
				// строки, по convention 0 при отсутствии — оставляем 0→1).
				newStart = op.newPos
			}
			newCount++
		}
	}
	if oldStart == 0 {
		oldStart = 1
	}
	if newStart == 0 {
		newStart = 1
	}
	fmt.Fprintf(sb, "@@ -%d,%d +%d,%d @@\n", oldStart, oldCount, newStart, newCount)
	for _, op := range ops {
		switch op.kind {
		case opEqual:
			sb.WriteString(" " + op.line + "\n")
		case opDel:
			sb.WriteString("-" + op.line + "\n")
		case opAdd:
			sb.WriteString("+" + op.line + "\n")
		}
	}
}

// splitLines разбивает текст на строки без завершающих \n (последняя
// строка без перевода — тоже строка).
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}
