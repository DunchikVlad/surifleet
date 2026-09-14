package ruleset

import (
	"testing"
)

func TestRenderDeterministic(t *testing.T) {
	a := []RawRule{
		{SID: 3, Raw: `alert tcp any any -> any any (msg:"c"; sid:3;)`},
		{SID: 1, Raw: `alert tcp any any -> any any (msg:"a"; sid:1;)`},
		{SID: 2, Raw: `alert tcp any any -> any any (msg:"b"; sid:2;)`},
	}
	b := []RawRule{a[2], a[0], a[1]} // другой порядок
	ra, rb := Render(a), Render(b)
	if string(ra) != string(rb) {
		t.Errorf("рендер зависит от порядка входа:\n%s\n---\n%s", ra, rb)
	}
	if SHA256(ra) != SHA256(rb) {
		t.Errorf("хэш зависит от порядка входа")
	}
}

func TestRenderSortedAndClean(t *testing.T) {
	out := string(Render([]RawRule{
		{SID: 20, Raw: "  rule-b  "},
		{SID: 10, Raw: "rule-a"},
		{SID: 30, Raw: "   "}, // пустое — отбрасывается
	}))
	want := "rule-a\nrule-b\n"
	if out != want {
		t.Errorf("Render = %q, ожидалось %q", out, want)
	}
}

func TestSHA256AndKey(t *testing.T) {
	// Известный sha256 от "abc".
	got := SHA256([]byte("abc"))
	want := "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("SHA256 = %q, ожидалось %q", got, want)
	}
	if k := BlobKey(want); k != "rulesets/"+want+".rules" {
		t.Errorf("BlobKey = %q", k)
	}
}
