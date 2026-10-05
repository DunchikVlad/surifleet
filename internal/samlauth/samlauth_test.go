package samlauth

import (
	"testing"

	"github.com/crewjam/saml"
)

func assertionWith(nameID string, attrs map[string][]string) *saml.Assertion {
	a := &saml.Assertion{}
	a.Subject = &saml.Subject{}
	a.Subject.NameID = &saml.NameID{Value: nameID}
	st := saml.AttributeStatement{}
	for name, vals := range attrs {
		at := saml.Attribute{Name: name}
		for _, v := range vals {
			at.Values = append(at.Values, saml.AttributeValue{Value: v})
		}
		st.Attributes = append(st.Attributes, at)
	}
	a.AttributeStatements = []saml.AttributeStatement{st}
	return a
}

func TestConfigFromAndValidate(t *testing.T) {
	// Валидный конфиг по URL.
	c, err := ConfigFrom([]byte(`{"idp_metadata_url":"https://idp/metadata.xml","sp_entity_id":"https://sp","acs_url":"https://sp/acs"}`))
	if err != nil {
		t.Fatalf("ConfigFrom: %v", err)
	}
	if err := c.Validate(); err != nil {
		t.Errorf("Validate: %v", err)
	}
	// Inline XML вместо URL — тоже ок.
	c2, _ := ConfigFrom([]byte(`{"idp_metadata_xml":"<xml/>","sp_entity_id":"s","acs_url":"a"}`))
	if err := c2.Validate(); err != nil {
		t.Errorf("Validate (inline xml): %v", err)
	}
	// Ошибки: пусто / нет метаданных / нет entity/acs.
	if _, err := ConfigFrom(nil); err == nil {
		t.Error("пустой config должен давать ошибку")
	}
	if _, err := ConfigFrom([]byte("{bad")); err == nil {
		t.Error("битый JSON должен давать ошибку")
	}
	bad := Config{SPEntityID: "s", ACSURL: "a"} // нет метаданных
	if err := bad.Validate(); err == nil {
		t.Error("нет idp_metadata_* — ожидается ошибка")
	}
	bad2 := Config{IDPMetadataURL: "u", ACSURL: "a"} // нет entity id
	if err := bad2.Validate(); err == nil {
		t.Error("нет sp_entity_id — ожидается ошибка")
	}
	bad3 := Config{IDPMetadataURL: "u", SPEntityID: "s"} // нет acs
	if err := bad3.Validate(); err == nil {
		t.Error("нет acs_url — ожидается ошибка")
	}
}

func TestProfileFromAssertion(t *testing.T) {
	c := Config{}
	p, err := c.ProfileFromAssertion(assertionWith("user@corp", map[string][]string{
		"email":       {"ivan@corp.example"},
		"displayName": {"Ivan Petrov"},
		"groups":      {"sec-admins", "viewers", "sec-admins"}, // дубль
	}))
	if err != nil {
		t.Fatalf("ProfileFromAssertion: %v", err)
	}
	if p.ExternalID != "user@corp" {
		t.Errorf("externalID (NameID) = %q", p.ExternalID)
	}
	if p.Email != "ivan@corp.example" {
		t.Errorf("email = %q", p.Email)
	}
	if p.DisplayName != "Ivan Petrov" {
		t.Errorf("displayName = %q", p.DisplayName)
	}
	if len(p.Groups) != 2 || p.Groups[0] != "sec-admins" || p.Groups[1] != "viewers" {
		t.Errorf("groups (дедуп) = %v", p.Groups)
	}
}

func TestProfileFromAssertionCustomAttrsAndFallbacks(t *testing.T) {
	// Кастомные имена атрибутов + username_attr вместо NameID.
	c := Config{UsernameAttr: "uid", EmailAttr: "mail", NameAttr: "cn", GroupsAttr: "memberOf"}
	p, err := c.ProfileFromAssertion(assertionWith("nameid-x", map[string][]string{
		"uid": {"iv123"}, "mail": {"iv@corp"}, "cn": {"Ivan"}, "memberOf": {"g1"},
	}))
	if err != nil {
		t.Fatalf("ProfileFromAssertion: %v", err)
	}
	if p.ExternalID != "iv123" {
		t.Errorf("externalID (username_attr) = %q", p.ExternalID)
	}
	if p.Email != "iv@corp" || p.DisplayName != "Ivan" || len(p.Groups) != 1 {
		t.Errorf("profile = %+v", p)
	}
	// Нет displayName → email.
	c3 := Config{}
	p3, err := c3.ProfileFromAssertion(assertionWith("n", map[string][]string{"email": {"e@x"}}))
	if err != nil || p3.DisplayName != "e@x" {
		t.Errorf("displayName fallback = %q, err=%v", p3.DisplayName, err)
	}
}

func TestProfileFromAssertionNoEmail(t *testing.T) {
	c := Config{}
	_, err := c.ProfileFromAssertion(assertionWith("n", map[string][]string{"displayName": {"x"}}))
	if err == nil {
		t.Fatal("assertion без email — ожидается ошибка")
	}
	// Пустой assertion.
	if _, err := c.ProfileFromAssertion(nil); err == nil {
		t.Fatal("nil assertion — ожидается ошибка")
	}
}
