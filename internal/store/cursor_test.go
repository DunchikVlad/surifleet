package store

import (
	"testing"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	for i := 0; i < 100; i++ {
		id := uuid.New()
		got, err := DecodeCursor(EncodeCursor(id))
		if err != nil {
			t.Fatalf("DecodeCursor: %v", err)
		}
		if got != id {
			t.Fatalf("round-trip: хочу %s, получил %s", id, got)
		}
	}
}

func TestDecodeCursorEmpty(t *testing.T) {
	id, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("пустой курсор не должен быть ошибкой: %v", err)
	}
	if id != uuid.Nil {
		t.Fatalf("пустой курсор должен давать uuid.Nil, получил %s", id)
	}
}

func TestDecodeCursorGarbage(t *testing.T) {
	for _, s := range []string{"!!!", "not-base64!!!", "aGVsbG8", "AAAA"} {
		if _, err := DecodeCursor(s); err == nil {
			t.Fatalf("мусорный курсор %q должен давать ошибку", s)
		}
	}
}

func TestEncodeCursorURLSafe(t *testing.T) {
	c := EncodeCursor(uuid.New())
	for _, r := range c {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
		default:
			t.Fatalf("курсор %q содержит небезопасный для URL символ %q", c, r)
		}
	}
}
