package ldapauth

import (
	"testing"

	"github.com/go-ldap/ldap/v3"
)

func TestBuildUserFilter(t *testing.T) {
	// Дефолтный фильтр — AD/POSIX набор с экранированным username.
	c := Config{}
	f := c.BuildUserFilter("ivan.petrov")
	want := "(|(sAMAccountName=ivan.petrov)(userPrincipalName=ivan.petrov)(uid=ivan.petrov))"
	if f != want {
		t.Errorf("дефолтный фильтр = %q, ожидается %q", f, want)
	}
	// Шаблон из конфига.
	c2 := Config{UserFilter: "(&(objectClass=person)(sAMAccountName=%s))"}
	if got := c2.BuildUserFilter("user1"); got != "(&(objectClass=person)(sAMAccountName=user1))" {
		t.Errorf("шаблон = %q", got)
	}
}

func TestBuildUserFilterEscapesInjection(t *testing.T) {
	c := Config{}
	// Символы LDAP-инъекции должны экранироваться (защита от обхода фильтра).
	f := c.BuildUserFilter("a)(|(uid=*")
	for _, bad := range []string{")(|(uid=*", "a)(|("} {
		if containsRaw(f, bad) {
			t.Errorf("инъекция не экранирована: %q содержит %q", f, bad)
		}
	}
	// * тоже экранируется (wildcard-обход).
	f2 := c.BuildUserFilter("ad*min")
	if containsRaw(f2, "ad*min") {
		t.Errorf("wildcard не экранирован: %q", f2)
	}
}

func containsRaw(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func entryWith(attrs map[string][]string) *ldap.Entry {
	e := ldap.NewEntry("cn=Ivan Petrov,ou=users,dc=corp,dc=example,dc=com", attrs)
	return e
}

func TestProfileFromEntry(t *testing.T) {
	c := Config{}
	p, err := c.ProfileFromEntry(entryWith(map[string][]string{
		"mail":        {"ivan@corp.example"},
		"displayName": {"Ivan Petrov"},
		"memberOf":    {"cn=sec-admins,ou=groups,dc=corp,dc=example,dc=com", "cn=viewers,ou=groups,dc=corp,dc=example,dc=com"},
	}))
	if err != nil {
		t.Fatalf("ProfileFromEntry: %v", err)
	}
	if p.Email != "ivan@corp.example" {
		t.Errorf("email = %q", p.Email)
	}
	if p.DisplayName != "Ivan Petrov" {
		t.Errorf("displayName = %q", p.DisplayName)
	}
	if p.ExternalID != "cn=Ivan Petrov,ou=users,dc=corp,dc=example,dc=com" {
		t.Errorf("externalID (DN) = %q", p.ExternalID)
	}
	if len(p.Groups) != 2 || p.Groups[0] != "sec-admins" || p.Groups[1] != "viewers" {
		t.Errorf("groups = %v", p.Groups)
	}
}

func TestProfileFromEntryUPNAndCNFallback(t *testing.T) {
	c := Config{}
	// Нет mail → userPrincipalName; нет displayName → cn → email.
	p, err := c.ProfileFromEntry(entryWith(map[string][]string{
		"userPrincipalName": {"ivan@corp.local"},
		"cn":                {"Ivan P"},
	}))
	if err != nil {
		t.Fatalf("ProfileFromEntry: %v", err)
	}
	if p.Email != "ivan@corp.local" {
		t.Errorf("email (UPN fallback) = %q", p.Email)
	}
	if p.DisplayName != "Ivan P" {
		t.Errorf("displayName (cn fallback) = %q", p.DisplayName)
	}
}

func TestProfileFromEntryNoEmail(t *testing.T) {
	c := Config{}
	_, err := c.ProfileFromEntry(entryWith(map[string][]string{"cn": {"x"}}))
	if err != ErrNoEmail {
		t.Fatalf("нет email → ожидается ErrNoEmail, получено %v", err)
	}
}

func TestGroupNamesFromEntryDedupAndNonDN(t *testing.T) {
	c := Config{}
	groups := c.GroupNamesFromEntry(entryWith(map[string][]string{
		"memberOf": {
			"cn=sec-admins,ou=groups,dc=corp,dc=example,dc=com",
			"cn=sec-admins,ou=groups,dc=corp,dc=example,dc=com", // дубль
			"cn=viewers,ou=groups,dc=corp,dc=example,dc=com",
		},
	}))
	if len(groups) != 2 {
		t.Fatalf("дедуп групп = %v, ожидается 2", groups)
	}
	// Не-DN значение (posix-группа) — как есть.
	c2 := Config{GroupAttr: "gidNumber"}
	g2 := c2.GroupNamesFromEntry(entryWith(map[string][]string{"gidNumber": {"1001"}}))
	if len(g2) != 1 || g2[0] != "1001" {
		t.Errorf("posix-группа = %v", g2)
	}
}

func TestConfigFrom(t *testing.T) {
	raw := []byte(`{"url":"ldaps://dc.corp.example:636","base_dn":"dc=corp,dc=example,dc=com","bind_dn":"cn=svc,ou=svc,dc=corp","bind_password":"x"}`)
	c, err := ConfigFrom(raw)
	if err != nil {
		t.Fatalf("ConfigFrom: %v", err)
	}
	if c.URL != "ldaps://dc.corp.example:636" || c.BaseDN != "dc=corp,dc=example,dc=com" {
		t.Errorf("config = %+v", c)
	}
	// Пустой raw — ошибка.
	if _, err := ConfigFrom(nil); err == nil {
		t.Fatal("пустой config должен давать ошибку")
	}
	// Битый JSON — ошибка.
	if _, err := ConfigFrom([]byte("{bad")); err == nil {
		t.Fatal("битый JSON должен давать ошибку")
	}
}
