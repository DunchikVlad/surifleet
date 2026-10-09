package textdiff

import (
	"strings"
	"testing"
)

func TestUnifiedIdentical(t *testing.T) {
	if d := Unified("a\nb\n", "a\nb\n", "old", "new", 3); d != "" {
		t.Fatalf("идентичные тексты — diff должен быть пустым, получено:\n%s", d)
	}
}

func TestUnifiedChange(t *testing.T) {
	a := "one\ntwo\nthree\nfour\nfive\n"
	b := "one\ntwo\nTHREE\nfour\nfive\n"
	d := Unified(a, b, "v1", "v2", 1)
	for _, want := range []string{
		"--- v1\n", "+++ v2\n",
		"@@ -2,3 +2,3 @@\n",
		" two\n", "-three\n", "+THREE\n", " four\n",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("diff не содержит %q:\n%s", want, d)
		}
	}
	// Строка «five» за пределами контекста 1 — не должна попасть.
	if strings.Contains(d, "five") {
		t.Errorf("контекст=1, строка five лишняя:\n%s", d)
	}
}

func TestUnifiedAddOnly(t *testing.T) {
	d := Unified("a\n", "a\nb\nc\n", "x", "y", 3)
	if !strings.Contains(d, "+b") || !strings.Contains(d, "+c") {
		t.Fatalf("добавленные строки не в diff:\n%s", d)
	}
	if strings.Contains(d, "-a") {
		t.Errorf("удалений нет, -a лишний:\n%s", d)
	}
}

func TestUnifiedDelOnly(t *testing.T) {
	d := Unified("a\nb\nc\n", "a\n", "x", "y", 3)
	if !strings.Contains(d, "-b") || !strings.Contains(d, "-c") {
		t.Fatalf("удалённые строки не в diff:\n%s", d)
	}
	if strings.Contains(d, "+") && !strings.Contains(d, "+++ y") {
		t.Errorf("добавлений нет:\n%s", d)
	}
}

func TestUnifiedEmptySides(t *testing.T) {
	d := Unified("", "l1\nl2\n", "e", "f", 3)
	if !strings.Contains(d, "@@ -1,0 +1,2 @@") && !strings.Contains(d, "@@ -0,0 +1,2 @@") {
		t.Errorf("ожидался hunk добавления двух строк:\n%s", d)
	}
	if !strings.Contains(d, "+l1\n+l2\n") {
		t.Errorf("строки добавления:\n%s", d)
	}
}

func TestUnifiedHunkMerge(t *testing.T) {
	// Два изменения через 4 совпадающие строки (≤ 2*context при context=3)
	// — один hunk.
	a := "1\n2\n3\n4\n5\n6\n7\n8\n"
	b := "1\nX\n3\n4\n5\n6\nY\n8\n"
	d := Unified(a, b, "a", "b", 3)
	if strings.Count(d, "@@") != 2 { // один hunk = два «@@» в заголовке
		t.Errorf("ожидался один hunk (слияние), получено:\n%s", d)
	}
}

func TestUnifiedHunkSplit(t *testing.T) {
	// Два изменения через 8 совпадающих строк (> 2*context при context=2)
	// — два hunks.
	lines := []string{"h1"}
	for i := 0; i < 8; i++ {
		lines = append(lines, "mid")
	}
	lines = append(lines, "h2")
	a := strings.Join(lines, "\n") + "\n"
	linesB := append([]string{}, lines...)
	linesB[0] = "H1"
	linesB[len(linesB)-1] = "H2"
	b := strings.Join(linesB, "\n") + "\n"
	d := Unified(a, b, "a", "b", 2)
	if strings.Count(d, "@@") != 4 {
		t.Errorf("ожидалось два hunks, получено:\n%s", d)
	}
}

func TestUnifiedHugeDegrades(t *testing.T) {
	// Деградация сверх maxCells: просто проверяем, что не падает и даёт
	// +/- строки (таблицу LCS не строим — 3000×3000=9M > 4M).
	n := 3000
	la := make([]string, n)
	lb := make([]string, n)
	for i := range la {
		la[i] = "a"
		lb[i] = "b"
	}
	d := Unified(strings.Join(la, "\n"), strings.Join(lb, "\n"), "x", "y", 3)
	if !strings.Contains(d, "-a") || !strings.Contains(d, "+b") {
		t.Errorf("деградированный diff должен содержать удаления/добавления")
	}
}
