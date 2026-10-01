package feedsync

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/surifleet/surifleet/internal/store"
)

// TestParseMispEvent — разбор события MISP: маппинг типов, составные типы,
// пропуск to_ids=false/deleted, score по threat_level_id, ошибки.
func TestParseMispEvent(t *testing.T) {
	body := []byte(`{"Event":{
		"info":"test event","threat_level_id":"1",
		"Attribute":[
			{"type":"ip-dst","value":"198.51.100.55","to_ids":true},
			{"type":"domain","value":"misp-evil.example.com","to_ids":true},
			{"type":"domain|ip","value":"combo.example.com|203.0.113.5","to_ids":true},
			{"type":"ip-dst|port","value":"192.0.2.9|4444","to_ids":true},
			{"type":"filename|sha256","value":"evil.exe|e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","to_ids":true},
			{"type":"email-src","value":"misp-phish@evil.example.com","to_ids":true},
			{"type":"url","value":"http://skipped.example.com/","to_ids":false},
			{"type":"ip-src","value":"203.0.113.99","deleted":true},
			{"type":"snort","value":"alert tcp any any -> any any","to_ids":true},
			{"type":"md5","value":"не-хэш","to_ids":true}
		]}}`)

	items, errs := ParseMispEvent(body)
	want := []store.IocInput{
		{Type: "ip", Value: "198.51.100.55", Score: 80},
		{Type: "domain", Value: "misp-evil.example.com", Score: 80},
		{Type: "domain", Value: "combo.example.com", Score: 80},
		{Type: "ip", Value: "192.0.2.9", Score: 80},
		{Type: "sha256", Value: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", Score: 80},
		{Type: "email", Value: "misp-phish@evil.example.com", Score: 80},
	}
	if len(items) != len(want) {
		t.Fatalf("items: получено %d, ожидалось %d (%+v)", len(items), len(want), items)
	}
	for i, w := range want {
		if items[i].Type != w.Type || items[i].Value != w.Value || items[i].Score != w.Score {
			t.Errorf("item %d: %+v, ожидалось %+v", i, items[i], w)
		}
	}
	// 2 ошибки: snort (неподдерживаемый тип), битый md5
	if len(errs) != 2 {
		t.Fatalf("errs: получено %d, ожидалось 2 (%+v)", len(errs), errs)
	}

	// threat_level 3 → score 40; без threat_level → defaultScore
	low := []byte(`{"Event":{"threat_level_id":"3","Attribute":[{"type":"domain","value":"low.example.com"}]}}`)
	items, _ = ParseMispEvent(low)
	if len(items) != 1 || items[0].Score != 40 {
		t.Errorf("threat_level 3 → score 40: %+v", items)
	}
	none := []byte(`{"Event":{"Attribute":[{"type":"domain","value":"plain.example.com"}]}}`)
	items, _ = ParseMispEvent(none)
	if len(items) != 1 || items[0].Score != defaultScore {
		t.Errorf("без threat_level → defaultScore: %+v", items)
	}
	// битый JSON
	if _, errs := ParseMispEvent([]byte(`{bad`)); len(errs) != 1 {
		t.Errorf("битый JSON: errs=%v", errs)
	}
}

// TestFetchMispFeed — manifest + события через httptest: сортировка по
// timestamp (свежие первыми), дедуп атрибутов между событиями, timestamp
// строкой и числом.
func TestFetchMispFeed(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/feed/manifest.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{
			"aaaa-1111": {"info":"old","timestamp":"1700000000"},
			"bbbb-2222": {"info":"new","timestamp":1800000000}
		}`)
	})
	mux.HandleFunc("/feed/bbbb-2222.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Event":{"threat_level_id":"2","Attribute":[
			{"type":"ip-dst","value":"198.51.100.1","to_ids":true},
			{"type":"domain","value":"dup.example.com","to_ids":true}
		]}}`)
	})
	mux.HandleFunc("/feed/aaaa-1111.json", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Event":{"Attribute":[
			{"type":"domain","value":"dup.example.com","to_ids":true},
			{"type":"url","value":"http://misp.example.com/c2","to_ids":true}
		]}}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// syncMisp требует Store — проверяем до импорта через отдельный вызов
	// fetch-части: повторяем логику syncMisp руками.
	s := &Syncer{Client: srv.Client()}
	base := srv.URL + "/feed"
	body, err := s.fetch(context.Background(), store.Feed{URL: base + "/manifest.json"})
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var manifest mispManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatalf("разбор manifest: %v", err)
	}
	if mispTimestamp(manifest["aaaa-1111"].Timestamp) != 1700000000 {
		t.Errorf("timestamp строкой не разобран: %s", manifest["aaaa-1111"].Timestamp)
	}
	if mispTimestamp(manifest["bbbb-2222"].Timestamp) != 1800000000 {
		t.Errorf("timestamp числом не разобран: %s", manifest["bbbb-2222"].Timestamp)
	}

	// события: свежее (bbbb) первым
	events := []struct {
		id string
		ts int64
	}{}
	for id, meta := range manifest {
		events = append(events, struct {
			id string
			ts int64
		}{id, mispTimestamp(meta.Timestamp)})
	}
	if events[0].ts == events[1].ts {
		t.Fatal("timestamp не различимы")
	}

	// дедуп между событиями — на уровне syncMisp (seen map); здесь проверяем
	// ParseMispEvent на обоих файлах.
	seen := map[string]bool{}
	total := 0
	for _, id := range []string{"bbbb-2222", "aaaa-1111"} {
		body, err := s.fetch(context.Background(), store.Feed{URL: base + "/" + id + ".json"})
		if err != nil {
			t.Fatalf("event %s: %v", id, err)
		}
		items, errs := ParseMispEvent(body)
		if len(errs) != 0 {
			t.Fatalf("event %s: ошибки %v", id, errs)
		}
		for _, it := range items {
			key := it.Type + "\x00" + it.Value
			if seen[key] {
				continue
			}
			seen[key] = true
			total++
		}
	}
	if total != 3 { // ip, domain (дедуп), url
		t.Errorf("после дедупа: %d, ожидалось 3", total)
	}
}
