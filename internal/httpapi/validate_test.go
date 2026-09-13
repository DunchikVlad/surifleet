package httpapi

import "testing"

func TestValidSlug(t *testing.T) {
	ok := []string{"a", "acme", "acme-corp", "org-1", "1st"}
	for _, s := range ok {
		if !validSlug(s) {
			t.Errorf("slug %q должен быть валидным", s)
		}
	}
	bad := []string{"", "A", "-acme", "acme-", "acme_corp", "acme corp", "АКМЕ",
		"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	for _, s := range bad {
		if validSlug(s) {
			t.Errorf("slug %q должен быть невалидным", s)
		}
	}
}

func TestValidName(t *testing.T) {
	if !validName("Acme Corp") {
		t.Error("обычное имя должно быть валидным")
	}
	for _, s := range []string{"", "   "} {
		if validName(s) {
			t.Errorf("имя %q должно быть невалидным", s)
		}
	}
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	if validName(string(long)) {
		t.Error("имя длиной 201 должно быть невалидным")
	}
}

func TestValidHostname(t *testing.T) {
	ok := []string{"web-01", "sensor1.example.com", "HOST"}
	for _, s := range ok {
		if !validHostname(s) {
			t.Errorf("hostname %q должен быть валидным", s)
		}
	}
	bad := []string{"", "-bad", "bad-", "bad_host", "bad host"}
	for _, s := range bad {
		if validHostname(s) {
			t.Errorf("hostname %q должен быть невалидным", s)
		}
	}
}

func TestValidIP(t *testing.T) {
	for _, s := range []string{"192.168.31.28", "10.0.0.1", "::1", "fd00::1"} {
		if !validIP(s) {
			t.Errorf("IP %q должен быть валидным", s)
		}
	}
	for _, s := range []string{"", "999.1.1.1", "192.168.31.28/24", "host"} {
		if validIP(s) {
			t.Errorf("IP %q должен быть невалидным", s)
		}
	}
}
