package httpapi

import (
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

func TestParsePageDefaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/organizations", nil)
	rec := httptest.NewRecorder()
	cursor, limit, ok := parsePage(rec, r)
	if !ok {
		t.Fatal("дефолтные параметры должны разбираться")
	}
	if limit != defaultLimit {
		t.Errorf("limit: хочу %d, получил %d", defaultLimit, limit)
	}
	if cursor != uuid.Nil {
		t.Errorf("cursor: хочу Nil, получил %s", cursor)
	}
}

func TestParsePageLimit(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?limit=1", nil)
	rec := httptest.NewRecorder()
	if _, limit, ok := parsePage(rec, r); !ok || limit != 1 {
		t.Errorf("limit=1: ok=%v limit=%d", ok, limit)
	}

	// Верхняя граница: 5000 ужимается до maxLimit.
	r = httptest.NewRequest("GET", "/x?limit=5000", nil)
	rec = httptest.NewRecorder()
	if _, limit, ok := parsePage(rec, r); !ok || limit != maxLimit {
		t.Errorf("limit=5000: ok=%v limit=%d (хочу %d)", ok, limit, maxLimit)
	}
}

func TestParsePageBadLimit(t *testing.T) {
	for _, q := range []string{"limit=abc", "limit=0", "limit=-5"} {
		r := httptest.NewRequest("GET", "/x?"+q, nil)
		rec := httptest.NewRecorder()
		if _, _, ok := parsePage(rec, r); ok {
			t.Errorf("%s должен давать ошибку валидации", q)
		}
		if rec.Code != 400 {
			t.Errorf("%s: статус хочу 400, получил %d", q, rec.Code)
		}
		errObj := decodeErrorBody(t, rec)
		if errObj["code"] != CodeValidation {
			t.Errorf("%s: code хочу %q, получил %v", q, CodeValidation, errObj["code"])
		}
	}
}

func TestParsePageCursor(t *testing.T) {
	id := uuid.New()
	r := httptest.NewRequest("GET", "/x?cursor="+store.EncodeCursor(id), nil)
	rec := httptest.NewRecorder()
	cursor, _, ok := parsePage(rec, r)
	if !ok {
		t.Fatal("валидный курсор должен разбираться")
	}
	if cursor != id {
		t.Errorf("cursor: хочу %s, получил %s", id, cursor)
	}
}

func TestParsePageBadCursor(t *testing.T) {
	r := httptest.NewRequest("GET", "/x?cursor=not-base64!!!", nil)
	rec := httptest.NewRecorder()
	if _, _, ok := parsePage(rec, r); ok {
		t.Error("битый курсор должен давать ошибку валидации")
	}
	errObj := decodeErrorBody(t, rec)
	if errObj["code"] != CodeValidation {
		t.Errorf("code: хочу %q, получил %v", CodeValidation, errObj["code"])
	}
}
