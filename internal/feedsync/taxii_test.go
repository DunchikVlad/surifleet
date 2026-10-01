package feedsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

func stixObj(t *testing.T, body string) json.RawMessage {
	t.Helper()
	return json.RawMessage(body)
}

// TestParseStix — разбор STIX-индикаторов: маппинг типов, confidence→score,
// valid_until→expires_at, пропуск revoked/истёкших/не-indicator, составные
// OR-паттерны, дедупликация, ошибки разбора.
func TestParseStix(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	future := now.Add(24 * time.Hour)
	past := now.Add(-24 * time.Hour)

	objects := []json.RawMessage{
		// простой ipv4 с confidence
		stixObj(t, `{"type":"indicator","pattern":"[ipv4-addr:value = '198.51.100.10']","pattern_type":"stix","confidence":80}`),
		// составной OR: домен + url
		stixObj(t, `{"type":"indicator","pattern":"[domain-name:value = 'evil.example.com' OR url:value = 'http://evil.example.com/p']"}`),
		// хэши с кавычками и без
		stixObj(t, `{"type":"indicator","pattern":"[file:hashes.'SHA-256' = 'e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855' OR file:hashes.MD5 = 'd41d8cd98f00b204e9800998ecf8427e']"}`),
		// email + valid_until в будущем → expires_at
		stixObj(t, fmt.Sprintf(`{"type":"indicator","pattern":"[email-addr:value = 'phish@evil.example.com']","valid_until":%q}`, future.Format(time.RFC3339))),
		// revoked — молча пропускается
		stixObj(t, `{"type":"indicator","pattern":"[ipv4-addr:value = '203.0.113.1']","revoked":true}`),
		// истёкший valid_until — молча пропускается
		stixObj(t, fmt.Sprintf(`{"type":"indicator","pattern":"[ipv4-addr:value = '203.0.113.2']","valid_until":%q}`, past.Format(time.RFC3339))),
		// не indicator — молча пропускается
		stixObj(t, `{"type":"identity","name":"ACME"}`),
		// pattern_type != stix — ошибка разбора
		stixObj(t, `{"type":"indicator","pattern":"alert http any any -> any any (msg:\"x\";)","pattern_type":"suricata"}`),
		// паттерн без поддерживаемых сравнений — ошибка разбора
		stixObj(t, `{"type":"indicator","pattern":"[network-traffic:dst_port = 443]"}`),
		// битый JSON — ошибка разбора
		json.RawMessage(`{not json`),
		// дубликат первого — схлопывается
		stixObj(t, `{"type":"indicator","pattern":"[ipv4-addr:value = '198.51.100.10']"}`),
	}

	items, errs := ParseStix(objects, now)

	want := []store.IocInput{
		{Type: "ip", Value: "198.51.100.10", Score: 80},
		{Type: "domain", Value: "evil.example.com", Score: defaultScore},
		{Type: "url", Value: "http://evil.example.com/p", Score: defaultScore},
		{Type: "sha256", Value: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Score: defaultScore},
		{Type: "md5", Value: "d41d8cd98f00b204e9800998ecf8427e", Score: defaultScore},
		{Type: "email", Value: "phish@evil.example.com", Score: defaultScore},
	}
	if len(items) != len(want) {
		t.Fatalf("items: получено %d, ожидалось %d (%+v)", len(items), len(want), items)
	}
	for i, w := range want {
		if items[i].Type != w.Type || items[i].Value != w.Value || items[i].Score != w.Score {
			t.Errorf("item %d: %+v, ожидалось %+v", i, items[i], w)
		}
	}
	// valid_until будущего email → ExpiresAt
	if items[5].ExpiresAt == nil || !items[5].ExpiresAt.Equal(future) {
		t.Errorf("expires_at: %+v, ожидалось %v", items[5].ExpiresAt, future)
	}
	if items[0].ExpiresAt != nil {
		t.Errorf("expires_at у ip должен быть nil, %+v", items[0].ExpiresAt)
	}
	// 3 ошибки разбора: suricata pattern_type, dst_port, битый JSON
	if len(errs) != 3 {
		t.Fatalf("errs: получено %d, ожидалось 3 (%+v)", len(errs), errs)
	}
}

// TestFetchTaxiiObjects — загрузка с httptest: прямой URL коллекции с
// пагинацией more/next, discovery через API root, Basic-auth из credentials.
func TestFetchTaxiiObjects(t *testing.T) {
	var gotAuth, gotAccept []string

	mux := http.NewServeMux()
	// API root: список коллекций
	mux.HandleFunc("/taxii2/collections/", func(w http.ResponseWriter, r *http.Request) {
		gotAuth = append(gotAuth, r.Header.Get("Authorization"))
		gotAccept = append(gotAccept, r.Header.Get("Accept"))
		fmt.Fprint(w, `{"collections":[{"id":"col-1","can_read":true},{"id":"col-2","can_read":false}]}`)
	})
	// Коллекция 1: две страницы (more/next)
	mux.HandleFunc("/taxii2/collections/col-1/objects/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("next") == "" {
			fmt.Fprint(w, `{"more":true,"next":"p2","objects":[{"type":"indicator","pattern":"[ipv4-addr:value = '198.51.100.1']"}]}`)
		} else {
			fmt.Fprint(w, `{"more":false,"objects":[{"type":"indicator","pattern":"[ipv4-addr:value = '198.51.100.2']"}]}`)
		}
	})
	// Прямая коллекция: одна страница
	mux.HandleFunc("/custom/collections/col-9/objects/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"objects":[{"type":"indicator","pattern":"[domain-name:value = 'x.example.com']"}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	s := &Syncer{Client: srv.Client()}
	creds := "user:pass"

	// API root: discovery → только col-1 (col-2 can_read=false), 2 страницы
	feed := store.Feed{ID: uuid.New(), Type: "taxii", URL: srv.URL + "/taxii2/", CredentialsRef: &creds}
	objs, err := s.fetchTaxiiObjects(context.Background(), feed)
	if err != nil {
		t.Fatalf("fetchTaxiiObjects (api root): %v", err)
	}
	if len(objs) != 2 {
		t.Fatalf("объектов: %d, ожидалось 2", len(objs))
	}
	if gotAuth[0] != "Basic dXNlcjpwYXNz" {
		t.Errorf("Authorization: %q, ожидался Basic", gotAuth[0])
	}
	if gotAccept[0] != taxiiAccept {
		t.Errorf("Accept: %q, ожидался %q", gotAccept[0], taxiiAccept)
	}

	// Прямой URL коллекции (без /objects — добавляется)
	feed2 := store.Feed{ID: uuid.New(), Type: "taxii", URL: srv.URL + "/custom/collections/col-9"}
	objs, err = s.fetchTaxiiObjects(context.Background(), feed2)
	if err != nil {
		t.Fatalf("fetchTaxiiObjects (collection): %v", err)
	}
	if len(objs) != 1 {
		t.Fatalf("объектов: %d, ожидался 1", len(objs))
	}

	// Пустой URL — понятная ошибка
	feed3 := store.Feed{ID: uuid.New(), Type: "taxii"}
	if _, err := s.fetchTaxiiObjects(context.Background(), feed3); err == nil {
		t.Fatal("пустой URL: ожидалась ошибка")
	}
}
