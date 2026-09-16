package feedsync

import (
	"strings"
	"testing"
)

func TestParseRules(t *testing.T) {
	body := `# ET Open тестовый фид
# комментарий с пробелом — пропускаем

alert tcp $HOME_NET any -> $EXTERNAL_NET any (msg:"ET TEST Active rule"; sid:9930001; rev:1; classtype:trojan-activity;)
#alert tcp $HOME_NET any -> any 443 (msg:"ET TEST Disabled rule"; flow:established; sid:9930002; rev:3;)
# это не правило, а комментарий про alert без скобок
мусорная строка вообще
drop udp any any -> $HOME_NET 53 (msg:"ET TEST Continuation" \
	" continued"; sid:9930003; rev:1;)
`
	rules_, errs := ParseRules([]byte(body))
	if len(rules_) != 3 {
		t.Fatalf("rules = %d, want 3 (%+v)", len(rules_), rules_)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %d, want 1 (%+v)", len(errs), errs)
	}
	if errs[0].Line != 7 {
		t.Errorf("err line = %d, want 7 (мусорная строка)", errs[0].Line)
	}

	active := rules_[0]
	if active.Disabled {
		t.Errorf("active.Disabled = true, want false")
	}
	if active.SID != 9930001 || active.Rev != 1 || active.Msg != "ET TEST Active rule" {
		t.Errorf("active = sid %d rev %d msg %q", active.SID, active.Rev, active.Msg)
	}
	if active.Classtype != "trojan-activity" {
		t.Errorf("active.Classtype = %q", active.Classtype)
	}

	disabled := rules_[1]
	if !disabled.Disabled {
		t.Errorf("disabled.Disabled = false, want true")
	}
	if disabled.SID != 9930002 || disabled.Rev != 3 {
		t.Errorf("disabled = sid %d rev %d", disabled.SID, disabled.Rev)
	}
	if strings.HasPrefix(disabled.Raw, "#") {
		t.Errorf("disabled.Raw не должен содержать ведущий '#': %q", disabled.Raw)
	}

	cont := rules_[2]
	if cont.SID != 9930003 || cont.Action != "drop" {
		t.Errorf("continuation = sid %d action %q", cont.SID, cont.Action)
	}
	if !strings.Contains(cont.Msg, "Continuation") {
		t.Errorf("continuation msg = %q", cont.Msg)
	}
}

func TestParseRulesEmptyAndCommentsOnly(t *testing.T) {
	if r, e := ParseRules(nil); len(r) != 0 || len(e) != 0 {
		t.Errorf("nil body: rules=%d errs=%d", len(r), len(e))
	}
	r, e := ParseRules([]byte("\xef\xbb\xbf# только комментарий\n\n#alert не правило без скобок\n"))
	if len(r) != 0 || len(e) != 0 {
		t.Errorf("comments only: rules=%d errs=%d (нераспарсенный #alert — комментарий, не ошибка)", len(r), len(e))
	}
}

func TestParseRulesNoTrailingNewline(t *testing.T) {
	r, e := ParseRules([]byte(`alert ip any any -> any any (msg:"ET TEST Tail"; sid:9930004; rev:2;)`))
	if len(e) != 0 || len(r) != 1 || r[0].SID != 9930004 || r[0].Rev != 2 {
		t.Errorf("rules=%+v errs=%+v", r, e)
	}
}
