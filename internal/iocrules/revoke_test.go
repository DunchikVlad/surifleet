package iocrules

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// fakeRuleStore — подделка RuleStore: слоты sid → правило.
type fakeRuleStore struct {
	bySid   map[int64]store.Rule
	updates []store.RulePatch
}

func (f *fakeRuleStore) GetBySid(_ context.Context, _ uuid.UUID, sid int64) (store.Rule, error) {
	if r, ok := f.bySid[sid]; ok {
		return r, nil
	}
	return store.Rule{}, store.ErrNotFound
}

func (f *fakeRuleStore) Update(_ context.Context, id uuid.UUID, p store.RulePatch) (store.Rule, error) {
	for sid, r := range f.bySid {
		if r.ID == id {
			f.updates = append(f.updates, p)
			if p.Status != nil {
				r.Status = *p.Status
			}
			if p.Tags != nil {
				r.Tags = p.Tags
			}
			f.bySid[sid] = r
			return r, nil
		}
	}
	return store.Rule{}, store.ErrNotFound
}

func iocRule(sid int64, typ, value, status string) store.Rule {
	msg := MsgFor(typ, value)
	return store.Rule{ID: uuid.New(), SID: sid, Msg: &msg, Status: status, SourceType: "ioc", Tags: []string{}}
}

func TestRevokeForIocDisablesRule(t *testing.T) {
	sid := SidFor("ip", "198.51.100.23")
	fs := &fakeRuleStore{bySid: map[int64]store.Rule{sid: iocRule(sid, "ip", "198.51.100.23", "enabled")}}

	ok, err := RevokeForIoc(context.Background(), uuid.New(), "ip", "198.51.100.23", fs)
	if err != nil || !ok {
		t.Fatalf("revoke: ok=%v err=%v", ok, err)
	}
	r := fs.bySid[sid]
	if r.Status != "disabled" {
		t.Fatalf("статус %q, ожидали disabled", r.Status)
	}
	if !hasTag(r.Tags, RevokedTag) {
		t.Fatalf("нет тега %q в %v", RevokedTag, r.Tags)
	}

	// Повторный отзыв — no-op, тег не дублируется.
	ok, err = RevokeForIoc(context.Background(), uuid.New(), "ip", "198.51.100.23", fs)
	if err != nil || ok {
		t.Fatalf("повторный revoke: ok=%v err=%v", ok, err)
	}
	if len(fs.updates) != 1 {
		t.Fatalf("updates: %d, ожидали 1", len(fs.updates))
	}
}

func TestRevokeForIocNoRule(t *testing.T) {
	fs := &fakeRuleStore{bySid: map[int64]store.Rule{}}
	ok, err := RevokeForIoc(context.Background(), uuid.New(), "ip", "198.51.100.23", fs)
	if err != nil || ok {
		t.Fatalf("пустое хранилище: ok=%v err=%v", ok, err)
	}
}

func TestRevokeForIocCollisionProbe(t *testing.T) {
	// Базовый слот занят чужим правилом, наше — следующим пробингом.
	sid := SidFor("ip", "198.51.100.23")
	next := SidBase + (sid-SidBase+1)%SidRange
	other := "ET MALWARE чужое"
	fs := &fakeRuleStore{bySid: map[int64]store.Rule{
		sid:  {ID: uuid.New(), SID: sid, Msg: &other, Status: "enabled", SourceType: "file"},
		next: iocRule(next, "ip", "198.51.100.23", "enabled"),
	}}

	ok, err := RevokeForIoc(context.Background(), uuid.New(), "ip", "198.51.100.23", fs)
	if err != nil || !ok {
		t.Fatalf("revoke со сдвигом: ok=%v err=%v", ok, err)
	}
	if fs.bySid[sid].Status != "enabled" {
		t.Fatal("чужое правило тронуто")
	}
	if fs.bySid[next].Status != "disabled" {
		t.Fatal("наше правило не отключено")
	}
}

func TestRevokeForIocKeepsMsg(t *testing.T) {
	// msg не должен меняться — он ключ владения слотом в генераторе.
	sid := SidFor("domain", "evil-example.test")
	fs := &fakeRuleStore{bySid: map[int64]store.Rule{sid: iocRule(sid, "domain", "evil-example.test", "enabled")}}
	if _, err := RevokeForIoc(context.Background(), uuid.New(), "domain", "evil-example.test", fs); err != nil {
		t.Fatal(err)
	}
	if got := *fs.bySid[sid].Msg; got != MsgFor("domain", "evil-example.test") {
		t.Fatalf("msg изменён: %q", got)
	}
}
