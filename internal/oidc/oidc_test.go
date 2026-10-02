package oidc

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/surifleet/surifleet/internal/store"
)

func TestPkceVerifierFromState(t *testing.T) {
	// Детерминированность: один state → один верификатор.
	v1 := pkceVerifierFromState("state-abc")
	v2 := pkceVerifierFromState("state-abc")
	if v1 != v2 {
		t.Fatalf("верификатор не детерминирован: %q != %q", v1, v2)
	}
	if pkceVerifierFromState("other") == v1 {
		t.Fatal("разные state дали одинаковый верификатор")
	}
	// Формат: base64url без padding, 43 символа (32 байта SHA-256).
	if len(v1) != 43 {
		t.Fatalf("длина верификатора %d, ожидается 43", len(v1))
	}
	if _, err := base64.RawURLEncoding.DecodeString(v1); err != nil {
		t.Fatalf("верификатор не base64url: %v", err)
	}
	// Challenge = base64url(sha256(verifier)) — как ожидает oauth2.
	sum := sha256.Sum256([]byte(v1))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge == "" || challenge == v1 {
		t.Fatal("challenge не должен совпадать с верификатором")
	}
}

func TestGroupsFrom(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		want int
	}{
		{"массив any", map[string]any{"groups": []any{"a", "b", ""}}, 2},
		{"массив string", map[string]any{"groups": []string{"x", "y"}}, 2},
		{"одна строка", map[string]any{"groups": "single"}, 1},
		{"нет ключа", map[string]any{"email": "e@x"}, 0},
		{"пустой массив", map[string]any{"groups": []any{}}, 0},
		{"не строки в массиве", map[string]any{"groups": []any{1, 2.5, true}}, 0},
		{"null", map[string]any{"groups": nil}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := groupsFrom(tc.raw)
			if len(got) != tc.want {
				t.Fatalf("groupsFrom(%v) = %v (%d), ожидается %d", tc.raw, got, len(got), tc.want)
			}
		})
	}
}

func TestRoleIDsForGroups(t *testing.T) {
	r1 := uuid.New()
	r2 := uuid.New()
	mapping, _ := json.Marshal(map[string][]string{
		"sec-admins": {r1.String(), r2.String()},
		"viewers":    {r2.String()},
	})

	// Одна группа — её роли.
	got := roleIDsForGroups(mapping, []string{"viewers"})
	if len(got) != 1 || got[0] != r2 {
		t.Fatalf("viewers → %v, ожидается [%s]", got, r2)
	}
	// Несколько групп — объединение с дедупом (r2 в обеих).
	got = roleIDsForGroups(mapping, []string{"sec-admins", "viewers"})
	if len(got) != 2 {
		t.Fatalf("объединение → %v, ожидается 2 уникальные роли", got)
	}
	// Незнакомая группа — пусто.
	if got := roleIDsForGroups(mapping, []string{"unknown"}); len(got) != 0 {
		t.Fatalf("unknown → %v, ожидается пусто", got)
	}
	// Пустые группы / маппинг.
	if got := roleIDsForGroups(mapping, nil); len(got) != 0 {
		t.Fatalf("nil groups → %v", got)
	}
	if got := roleIDsForGroups(json.RawMessage("{}"), []string{"viewers"}); len(got) != 0 {
		t.Fatalf("пустой mapping → %v", got)
	}
	// Битый JSON / не-UUID — пропускаются.
	if got := roleIDsForGroups(json.RawMessage("{bad"), []string{"viewers"}); len(got) != 0 {
		t.Fatalf("битый mapping → %v", got)
	}
	bad, _ := json.Marshal(map[string][]string{"g": {"not-a-uuid", r1.String()}})
	got = roleIDsForGroups(bad, []string{"g"})
	if len(got) != 1 || got[0] != r1 {
		t.Fatalf("не-UUID пропущен → %v, ожидается [%s]", got, r1)
	}
}

func TestOauth2ConfigScopes(t *testing.T) {
	p := store.SsoProvider{Config: store.OIDCConfig{
		IssuerURL: "https://idp.example", ClientID: "cid", RedirectURL: "https://app/cb",
	}}
	ep := oauth2.Endpoint{AuthURL: "https://idp.example/auth", TokenURL: "https://idp.example/token"}

	// Дефолтные scopes, если не заданы.
	cfg := oauth2Config(p, ep)
	want := []string{gooidc.ScopeOpenID, "email", "profile"}
	if len(cfg.Scopes) != len(want) {
		t.Fatalf("дефолтные scopes %v, ожидается %v", cfg.Scopes, want)
	}
	for i := range want {
		if cfg.Scopes[i] != want[i] {
			t.Fatalf("scope[%d]=%q, ожидается %q", i, cfg.Scopes[i], want[i])
		}
	}
	// Явные scopes переопределяют дефолт.
	p.Config.Scopes = []string{"openid", "groups"}
	cfg = oauth2Config(p, ep)
	if len(cfg.Scopes) != 2 || cfg.Scopes[1] != "groups" {
		t.Fatalf("явные scopes %v", cfg.Scopes)
	}
	if cfg.ClientID != "cid" || cfg.RedirectURL != "https://app/cb" || cfg.Endpoint != ep {
		t.Fatalf("поля конфига: %+v", cfg)
	}
}
