// Package ldapauth — LDAP/AD-аутентификация для SuriFleet (чанк 40, п. 9 ТЗ:
// «LDAP/AD — опционально: bind-аутентификация и чтение групп»).
//
// Поток (provider type=ldap в sso_providers):
//  1. Поиск пользователя под service-аккаунтом (bind_dn/bind_password из
//     config) по user_filter (шаблон с %s → escaped username) в base_dn.
//  2. Bind-аутентификация: соединение с DN найденного пользователя + его
//     паролем — проверяет креды самим каталогом (пароль у нас не оседает).
//  3. Чтение email (mail/userPrincipalName) и групп (memberOf или
//     group_membership_attr) из записи пользователя.
//  4. JIT-провижининг и маппинг групп → роли — общий oidc.Service.Provision.
package ldapauth

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"github.com/surifleet/surifleet/internal/store"
)

// Таймауты соединения с каталогом.
const dialTimeout = 10 * time.Second

// Ошибки домена LDAP (HTTP-слой отображает на 401/403).
var (
	ErrUserNotFound  = errors.New("пользователь не найден в каталоге")
	ErrInvalidCreds  = errors.New("неверное имя пользователя или пароль")
	ErrMultipleUsers = errors.New("несколько пользователей по фильтру — уточните user_filter")
	ErrNoEmail       = errors.New("у записи нет email (mail/userPrincipalName)")
)

// Config — конфигурация LDAP-провайдера (sso_providers.config jsonb).
type Config struct {
	URL          string `json:"url"`                     // ldap://host:389 или ldaps://host:636
	StartTLS     bool   `json:"start_tls"`               // ldap:// + StartTLS перед bind
	InsecureTLS  bool   `json:"insecure_tls"`            // пропустить проверку сертификата (только тест!)
	BindDN       string `json:"bind_dn"`                 // service-аккаунт для поиска (пусто — анонимный)
	BindPassword string `json:"bind_password,omitempty"` // writeOnly
	BaseDN       string `json:"base_dn"`                 // база поиска, напр. "dc=corp,dc=example,dc=com"
	// UserFilter — фильтр поиска пользователя; %s — escaped username.
	// Пусто → "(|(sAMAccountName=%s)(userPrincipalName=%s)(uid=%s))".
	UserFilter string `json:"user_filter"`
	// UsernameAttr — атрибут логина для внешнего id (пусто — DN записи).
	UsernameAttr string `json:"username_attr"`
	// EmailAttr — атрибут email (пусто — mail, затем userPrincipalName).
	EmailAttr string `json:"email_attr"`
	// DisplayNameAttr — атрибут отображаемого имени (пусто — displayName/cn).
	DisplayNameAttr string `json:"display_name_attr"`
	// GroupAttr — атрибут членства (пусто — memberOf).
	GroupAttr string `json:"group_attr"`
}

// ConfigFrom преобразует sso_providers.config (jsonb) в Config.
func ConfigFrom(raw []byte) (Config, error) {
	var c Config
	if len(raw) == 0 {
		return c, errors.New("пустая конфигурация ldap-провайдера")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("разбор ldap config: %w", err)
	}
	return c, nil
}

// BuildUserFilter — итоговый фильтр поиска пользователя: дефолтный
// AD/POSIX-набор или шаблон; username экранируется (ldap.EscapeFilter —
// защита от LDAP-инъекций). Каждый глагол %s заполняется одним username.
func (c Config) BuildUserFilter(username string) string {
	esc := ldap.EscapeFilter(username)
	f := c.UserFilter
	if f == "" {
		f = "(|(sAMAccountName=%s)(userPrincipalName=%s)(uid=%s))"
	}
	n := strings.Count(f, "%s")
	args := make([]any, n)
	for i := range args {
		args[i] = esc
	}
	return fmt.Sprintf(f, args...)
}

// email — атрибуты email по приоритету.
func (c Config) emailAttrs() []string {
	if c.EmailAttr != "" {
		return []string{c.EmailAttr}
	}
	return []string{"mail", "userPrincipalName"}
}

// displayNameAttrs — атрибуты отображаемого имени по приоритету.
func (c Config) displayNameAttrs() []string {
	if c.DisplayNameAttr != "" {
		return []string{c.DisplayNameAttr}
	}
	return []string{"displayName", "cn"}
}

// groupAttr — атрибут членства в группах.
func (c Config) groupAttr() string {
	if c.GroupAttr != "" {
		return c.GroupAttr
	}
	return "memberOf"
}

// GroupNamesFromEntry — имена групп из записи пользователя: значения
// memberOf — DN'ы ("cn=sec-admins,ou=groups,dc=...") — берём RDN cn=.
// Прочие атрибуты (напр. posix group id) — как есть. Дедуп сохранён.
func (c Config) GroupNamesFromEntry(entry *ldap.Entry) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, dn := range entry.GetAttributeValues(c.groupAttr()) {
		name := dn
		if strings.Contains(dn, "=") {
			if parsed, err := ldap.ParseDN(dn); err == nil && len(parsed.RDNs) > 0 && len(parsed.RDNs[0].Attributes) > 0 {
				name = parsed.RDNs[0].Attributes[0].Value
			}
		}
		name = strings.TrimSpace(name)
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// Profile — профиль пользователя из каталога.
type Profile struct {
	ExternalID  string // username_attr или DN записи
	Email       string
	DisplayName string
	Groups      []string
}

// ProfileFromEntry — профиль из найденной LDAP-записи.
func (c Config) ProfileFromEntry(entry *ldap.Entry) (Profile, error) {
	p := Profile{}
	if c.UsernameAttr != "" {
		p.ExternalID = entry.GetAttributeValue(c.UsernameAttr)
	}
	if p.ExternalID == "" {
		p.ExternalID = entry.DN
	}
	for _, attr := range c.emailAttrs() {
		if v := entry.GetAttributeValue(attr); v != "" {
			p.Email = v
			break
		}
	}
	if p.Email == "" {
		return p, ErrNoEmail
	}
	for _, attr := range c.displayNameAttrs() {
		if v := entry.GetAttributeValue(attr); v != "" {
			p.DisplayName = v
			break
		}
	}
	if p.DisplayName == "" {
		p.DisplayName = p.Email
	}
	p.Groups = c.GroupNamesFromEntry(entry)
	return p, nil
}

// Dial — соединение с каталогом (+StartTLS по настройке).
func (c Config) dial() (*ldap.Conn, error) {
	if c.URL == "" {
		return nil, errors.New("ldap: пустой url провайдера")
	}
	conn, err := ldap.DialURL(c.URL, ldap.DialWithDialer(&net.Dialer{Timeout: dialTimeout}))
	if err != nil {
		return nil, fmt.Errorf("ldap: соединение с %s: %w", c.URL, err)
	}
	if c.StartTLS {
		if err := conn.StartTLS(&tls.Config{InsecureSkipVerify: c.InsecureTLS}); err != nil {
			conn.Close()
			return nil, fmt.Errorf("ldap: StartTLS: %w", err)
		}
	}
	return conn, nil
}

// Authenticate — bind-аутентификация username+password и чтение профиля
// (email, группы). Ошибки: ErrUserNotFound / ErrInvalidCreds / ErrNoEmail.
func (c Config) Authenticate(username, password string) (Profile, error) {
	if username == "" || password == "" {
		return Profile{}, ErrInvalidCreds
	}
	conn, err := c.dial()
	if err != nil {
		return Profile{}, err
	}
	defer conn.Close()

	// Поиск под service-аккаунтом (или анонимно, если bind_dn пуст).
	if c.BindDN != "" {
		if err := conn.Bind(c.BindDN, c.BindPassword); err != nil {
			return Profile{}, fmt.Errorf("ldap: bind service-аккаунтом: %w", err)
		}
	}
	attrs := append([]string{c.groupAttr()}, c.emailAttrs()...)
	attrs = append(attrs, c.displayNameAttrs()...)
	if c.UsernameAttr != "" {
		attrs = append(attrs, c.UsernameAttr)
	}
	sr, err := conn.Search(ldap.NewSearchRequest(
		c.BaseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 2, 0, false,
		c.BuildUserFilter(username), attrs, nil,
	))
	if err != nil {
		return Profile{}, fmt.Errorf("ldap: поиск пользователя: %w", err)
	}
	if len(sr.Entries) == 0 {
		return Profile{}, ErrUserNotFound
	}
	if len(sr.Entries) > 1 {
		return Profile{}, ErrMultipleUsers
	}
	entry := sr.Entries[0]

	// Bind-аутентификация: DN найденного пользователя + его пароль.
	userConn, err := c.dial()
	if err != nil {
		return Profile{}, err
	}
	defer userConn.Close()
	if err := userConn.Bind(entry.DN, password); err != nil {
		var lerr *ldap.Error
		if errors.As(err, &lerr) && lerr.ResultCode == ldap.LDAPResultInvalidCredentials {
			return Profile{}, ErrInvalidCreds
		}
		return Profile{}, fmt.Errorf("ldap: bind пользователем: %w", err)
	}

	return c.ProfileFromEntry(entry)
}

// SsoProviderConfig — ldap-Config из провайдера (config jsonb).
func SsoProviderConfig(p store.SsoProvider) (Config, error) {
	return ConfigFrom(p.ConfigRaw)
}
