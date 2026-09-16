package iocrules

import (
	"strings"
	"testing"
)

func TestSidDeterministicAndInRange(t *testing.T) {
	a := SidFor("ip", "198.51.100.23")
	b := SidFor("ip", "198.51.100.23")
	if a != b {
		t.Fatalf("sid недетерминирован: %d != %d", a, b)
	}
	if a < SidBase || a >= SidBase+SidRange {
		t.Fatalf("sid %d вне диапазона [%d, %d)", a, SidBase, SidBase+SidRange)
	}
	// Разные значения/типы почти наверняка дают разные sid.
	if SidFor("ip", "198.51.100.24") == a {
		t.Fatal("коллизия sid для соседних ip")
	}
	if SidFor("domain", "198.51.100.23") == a {
		t.Fatal("тип не участвует в хэше")
	}
}

func TestSidCaseInsensitiveForDomain(t *testing.T) {
	if SidFor("domain", "Evil-Example.TEST") != SidFor("domain", "evil-example.test") {
		t.Fatal("домен должен хэшироваться без учёта регистра")
	}
}

func TestRuleForIP(t *testing.T) {
	raw, ok, _ := RuleFor("ip", "198.51.100.23")
	if !ok {
		t.Fatal("ip должен генерироваться")
	}
	want := `alert ip 198.51.100.23 any -> any any (msg:"SuriFleet IOC ip 198.51.100.23"; sid:`
	if !strings.HasPrefix(raw, want) {
		t.Fatalf("неожиданное правило: %s", raw)
	}
	if !strings.HasSuffix(raw, "; rev:2;)") {
		t.Fatalf("нет rev: %s", raw)
	}
}

func TestRuleForIPCIDR(t *testing.T) {
	raw, ok, _ := RuleFor("ip", "203.0.113.0/24")
	if !ok || !strings.Contains(raw, "203.0.113.0/24") {
		t.Fatalf("cidr: %s ok=%v", raw, ok)
	}
}

func TestRuleForDomain(t *testing.T) {
	raw, ok, _ := RuleFor("domain", "evil-example.test")
	if !ok {
		t.Fatal("domain должен генерироваться")
	}
	if !strings.Contains(raw, `dns.query; content:"evil-example.test"; nocase;`) {
		t.Fatalf("нет dns.query content: %s", raw)
	}
}

func TestRuleForURL(t *testing.T) {
	raw, ok, _ := RuleFor("url", "http://evil-example.test:8080/payload/x?a=1")
	if !ok {
		t.Fatal("url должен генерироваться")
	}
	if !strings.Contains(raw, `http.host; content:"evil-example.test";`) {
		t.Fatalf("нет http.host: %s", raw)
	}
	if !strings.Contains(raw, `http.uri; content:"/payload/x";`) {
		t.Fatalf("нет http.uri: %s", raw)
	}
}

func TestRuleForURLRoot(t *testing.T) {
	raw, ok, _ := RuleFor("url", "https://evil-example.test/")
	if !ok {
		t.Fatal("url должен генерироваться")
	}
	if strings.Contains(raw, "http.uri") {
		t.Fatalf("для корня http.uri не нужен: %s", raw)
	}
}

func TestRuleForHashAndEmailSkipped(t *testing.T) {
	for _, typ := range []string{"md5", "sha1", "sha256", "email"} {
		if _, ok, reason := RuleFor(typ, "whatever"); ok || reason == "" {
			t.Fatalf("%s: ожидался skip с причиной, ok=%v reason=%q", typ, ok, reason)
		}
	}
}

func TestEscaping(t *testing.T) {
	raw, ok, _ := RuleFor("domain", `ev";il.test`)
	if !ok {
		t.Fatal("генерация должна проходить")
	}
	if !strings.Contains(raw, `content:"ev\"\;il.test";`) {
		t.Fatalf("экранирование content не сработало: %s", raw)
	}
}
