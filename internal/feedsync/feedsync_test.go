package feedsync

import (
	"testing"
)

func TestGuessType(t *testing.T) {
	cases := map[string]string{
		"203.0.113.77":                             "ip",
		"2001:db8::1":                              "ip",
		"198.51.100.0/24":                          "ip",
		"malware.example.com":                      "domain",
		"http://evil.example.com/pay/load?a=1":     "url",
		"d41d8cd98f00b204e9800998ecf8427e":         "md5",
		"da39a3ee5e6b4b0d3255bfef95601890afd80709": "sha1",
		"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855": "sha256",
		"bot@evil.example.com": "email",
		"":                     "",
		"not an ioc!!":         "",
	}
	for v, want := range cases {
		if got := GuessType(v); got != want {
			t.Errorf("GuessType(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestParsePlainText(t *testing.T) {
	body := `# комментарий
203.0.113.77

malware.example.com
http://evil.example.com/x
// ещё комментарий
не валидно!!
d41d8cd98f00b204e9800998ecf8427e
`
	items, errs := Parse([]byte(body))
	if len(items) != 4 {
		t.Fatalf("items = %d, want 4 (%+v)", len(items), items)
	}
	if len(errs) != 1 {
		t.Fatalf("errs = %d, want 1 (%+v)", len(errs), errs)
	}
	if items[0].Type != "ip" || items[0].Value != "203.0.113.77" {
		t.Errorf("items[0] = %+v", items[0])
	}
	if items[0].Score != defaultScore {
		t.Errorf("score = %d, want %d", items[0].Score, defaultScore)
	}
	if errs[0].Line != 7 {
		t.Errorf("err line = %d, want 7", errs[0].Line)
	}
}

func TestParseCSV(t *testing.T) {
	body := "198.51.100.10,ip\ndomain,evil2.example.org\n"
	items, errs := Parse([]byte(body))
	if len(errs) != 0 {
		t.Fatalf("errs = %+v", errs)
	}
	if len(items) != 2 || items[0].Type != "ip" || items[1].Type != "domain" || items[1].Value != "evil2.example.org" {
		t.Fatalf("items = %+v", items)
	}
}

func TestParseJSON(t *testing.T) {
	body := `["203.0.113.99", {"type": "domain", "value": "json.example.com", "score": 90}, {"value": "broken !!"}]`
	items, errs := Parse([]byte(body))
	if len(items) != 2 {
		t.Fatalf("items = %+v", items)
	}
	if len(errs) != 1 || errs[0].Line != 3 {
		t.Fatalf("errs = %+v", errs)
	}
	if items[0].Type != "ip" || items[1].Score != 90 {
		t.Fatalf("items = %+v", items)
	}
}

func TestParseEmpty(t *testing.T) {
	items, errs := Parse([]byte("  \n# только комментарий\n"))
	if len(items) != 0 || len(errs) != 0 {
		t.Fatalf("items=%+v errs=%+v", items, errs)
	}
}
