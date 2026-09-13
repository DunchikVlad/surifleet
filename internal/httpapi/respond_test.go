package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/surifleet/surifleet/internal/store"
)

// decodeErrorBody достаёт {"error": {...}} из ответа.
func decodeErrorBody(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("ответ не JSON: %v (%q)", err, rec.Body.String())
	}
	errObj, ok := body["error"]
	if !ok {
		t.Fatalf("в ответе нет ключа error: %q", rec.Body.String())
	}
	return errObj
}

func TestWriteErrorFormat(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusBadRequest, CodeValidation, "плохой запрос", map[string]any{"field": "name"})

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("статус: хочу 400, получил %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("Content-Type: хочу application/json, получил %q", ct)
	}
	errObj := decodeErrorBody(t, rec)
	if errObj["code"] != CodeValidation {
		t.Errorf("code: хочу %q, получил %v", CodeValidation, errObj["code"])
	}
	if errObj["message"] != "плохой запрос" {
		t.Errorf("message: получил %v", errObj["message"])
	}
	details, ok := errObj["details"].(map[string]any)
	if !ok || details["field"] != "name" {
		t.Errorf("details: получил %v", errObj["details"])
	}
}

func TestWriteErrorNoDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	writeError(rec, http.StatusNotFound, CodeNotFound, "не найдено", nil)
	errObj := decodeErrorBody(t, rec)
	if _, present := errObj["details"]; present {
		t.Errorf("details должен отсутствовать (omitempty), получил %v", errObj["details"])
	}
}

func TestWriteStoreErrorMapping(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not_found", store.ErrNotFound, http.StatusNotFound, CodeNotFound},
		{"conflict", store.ErrConflict, http.StatusConflict, CodeConflict},
		{"conflict_wrapped", &store.ConflictError{Constraint: "organizations_slug_key"}, http.StatusConflict, CodeConflict},
		{"foreign_key", store.ErrForeignKey, http.StatusBadRequest, CodeValidation},
		{"wrapped_not_found", fmt.Errorf("repo: %w", store.ErrNotFound), http.StatusNotFound, CodeNotFound},
		{"unknown", errors.New("boom"), http.StatusInternalServerError, CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writeStoreError(rec, tc.err)
			if rec.Code != tc.wantStatus {
				t.Fatalf("статус: хочу %d, получил %d", tc.wantStatus, rec.Code)
			}
			errObj := decodeErrorBody(t, rec)
			if errObj["code"] != tc.wantCode {
				t.Errorf("code: хочу %q, получил %v", tc.wantCode, errObj["code"])
			}
		})
	}
}

func TestConflictErrorDetails(t *testing.T) {
	rec := httptest.NewRecorder()
	writeStoreError(rec, &store.ConflictError{Constraint: "clusters_organization_id_name_key"})
	errObj := decodeErrorBody(t, rec)
	details, ok := errObj["details"].(map[string]any)
	if !ok || details["constraint"] != "clusters_organization_id_name_key" {
		t.Errorf("details.constraint: получил %v", errObj["details"])
	}
}
