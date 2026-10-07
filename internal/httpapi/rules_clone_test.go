package httpapi

import (
	"strings"
	"testing"
)

func TestRewriteRuleRaw(t *testing.T) {
	raw := `alert http $HOME_NET any -> $EXTERNAL_NET any (msg:"ET TEST Some \"quoted\" rule"; flow:established,to_server; classtype:trojan-activity; sid:2000999; rev:5;)`

	got := rewriteRuleRaw(raw, 9000001, `My clone "v2"`)
	want := `alert http $HOME_NET any -> $EXTERNAL_NET any (msg:"My clone \"v2\""; flow:established,to_server; classtype:trojan-activity; sid:9000001; rev:1;)`
	if got != want {
		t.Errorf("rewrite:\n got: %s\nwant: %s", got, want)
	}

	// Пустой msg — не меняется; sid/rev меняются
	got2 := rewriteRuleRaw(raw, 9000002, "")
	if !strings.Contains(got2, `msg:"ET TEST Some \"quoted\" rule"`) || !strings.Contains(got2, "sid:9000002") || !strings.Contains(got2, "rev:1") {
		t.Errorf("пустой msg: %s", got2)
	}
	// msg с экранированными кавычками не ломает границу
	if strings.Contains(got, `Some \"quoted\"`) {
		t.Errorf("граница msg сломана экранированием: %s", got)
	}
}
