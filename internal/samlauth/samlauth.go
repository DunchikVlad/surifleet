// Package samlauth — SAML 2.0 SSO для SuriFleet (чанк 41, п. 9 ТЗ:
// «SAML 2.0 — второй протокол для корпоративных IdP»). Библиотека
// crewjam/saml (по ТЗ) — ServiceProvider, разбор и валидация assertion
// (подпись, условия, аудитория); самодельного XML/крипто нет.
//
// Поток (Web SSO, HTTP-Redirect/POST bindings):
//  1. GET /auth/saml/{id}/metadata — SP-метаданные (импортируют в IdP).
//  2. GET /auth/saml/{id}/login — AuthnRequest → 302 на SSO-endpoint IdP.
//  3. POST /auth/saml/acs — SAMLResponse (form_post) → проверка assertion
//     (подпись сертификатом IdP из его метаданных, сроки, audience) →
//     атрибуты (email/name/groups) → JIT-провижининг (общий oidc.Provision).
//
// SP-ключ/сертификат — самоподписанный, генерируется в памяти при старте
// процесса (MVP): метаданные SP действительны до рестарта сервера —
// постоянный ключ в конфиге/хранилище — следующий шаг.
package samlauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/crewjam/saml"
	"github.com/google/uuid"

	"github.com/surifleet/surifleet/internal/store"
)

// Config — конфигурация SAML-провайдера (sso_providers.config jsonb).
type Config struct {
	// IDPMetadataURL — URL метаданных IdP (скачивается лениво, кэшируется).
	IDPMetadataURL string `json:"idp_metadata_url"`
	// IDPMetadataXML — метаданные IdP inline (если URL недоступен).
	IDPMetadataXML string `json:"idp_metadata_xml"`
	// SPEntityID — entityID нашего Service Provider.
	SPEntityID string `json:"sp_entity_id"`
	// ACSURL — URL ACS (Assertion Consumer Service), регистрируется в IdP:
	// <базовый URL сервера>/api/v1/auth/saml/acs
	ACSURL string `json:"acs_url"`
	// Attrs — имена атрибутов assertion (дефолты — общепринятые).
	UsernameAttr string `json:"username_attr"` // пусто → NameID assertion
	EmailAttr    string `json:"email_attr"`    // пусто → "email"
	NameAttr     string `json:"name_attr"`     // пусто → "displayName"
	GroupsAttr   string `json:"groups_attr"`   // пусто → "groups"
}

// ConfigFrom — разбор jsonb конфига SAML-провайдера.
func ConfigFrom(raw []byte) (Config, error) {
	var c Config
	if len(raw) == 0 {
		return c, errors.New("пустая конфигурация saml-провайдера")
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("разбор saml config: %w", err)
	}
	return c, nil
}

// Validate — обязательные поля (для валидации в API).
func (c Config) Validate() error {
	if c.IDPMetadataURL == "" && c.IDPMetadataXML == "" {
		return errors.New("нужен idp_metadata_url или idp_metadata_xml")
	}
	if c.SPEntityID == "" {
		return errors.New("пустой sp_entity_id")
	}
	if c.ACSURL == "" {
		return errors.New("пустой acs_url")
	}
	return nil
}

// emailAttr / nameAttr / groupsAttr — атрибуты с дефолтами.
func (c Config) emailAttr() string {
	if c.EmailAttr != "" {
		return c.EmailAttr
	}
	return "email"
}
func (c Config) nameAttr() string {
	if c.NameAttr != "" {
		return c.NameAttr
	}
	return "displayName"
}
func (c Config) groupsAttr() string {
	if c.GroupsAttr != "" {
		return c.GroupsAttr
	}
	return "groups"
}

// Profile — профиль из assertion.
type Profile struct {
	ExternalID  string
	Email       string
	DisplayName string
	Groups      []string
}

// ProfileFromAssertion — профиль из SAML-assertion: NameID (или
// username_attr) → ExternalID; email/name/groups из атрибутов. Дедуп групп.
func (c Config) ProfileFromAssertion(a *saml.Assertion) (Profile, error) {
	if a == nil {
		return Profile{}, errors.New("пустой assertion")
	}
	p := Profile{ExternalID: a.Subject.NameID.Value}
	attr := func(name string) string {
		for _, st := range a.AttributeStatements {
			for _, at := range st.Attributes {
				if at.Name == name || at.FriendlyName == name {
					if len(at.Values) > 0 {
						return strings.TrimSpace(at.Values[0].Value)
					}
				}
			}
		}
		return ""
	}
	attrs := func(name string) []string {
		out := []string{}
		seen := map[string]bool{}
		for _, st := range a.AttributeStatements {
			for _, at := range st.Attributes {
				if at.Name != name && at.FriendlyName != name {
					continue
				}
				for _, v := range at.Values {
					s := strings.TrimSpace(v.Value)
					if s != "" && !seen[s] {
						seen[s] = true
						out = append(out, s)
					}
				}
			}
		}
		return out
	}
	if c.UsernameAttr != "" {
		if v := attr(c.UsernameAttr); v != "" {
			p.ExternalID = v
		}
	}
	p.Email = attr(c.emailAttr())
	if p.Email == "" {
		return p, errors.New("assertion без email-атрибута (" + c.emailAttr() + ")")
	}
	p.DisplayName = attr(c.nameAttr())
	if p.DisplayName == "" {
		p.DisplayName = p.Email
	}
	p.Groups = attrs(c.groupsAttr())
	return p, nil
}

// Service — SAML SP: ленивая сборка ServiceProvider на провайдера,
// самоподписанный ключ SP (общий на процесс).
type Service struct {
	hc *http.Client

	mu   sync.Mutex
	key  *rsa.PrivateKey
	cert *x509.Certificate
	sps  map[uuid.UUID]*saml.ServiceProvider
}

// NewService — SAML-сервис (HTTP-клиент для метаданных IdP).
func NewService() *Service {
	return &Service{
		hc:  &http.Client{Timeout: 15 * time.Second},
		sps: map[uuid.UUID]*saml.ServiceProvider{},
	}
}

// keyPair — самоподписанный ключ/сертификат SP (генерируется один раз на
// процесс; MVP — действителен до рестарта сервера).
func (s *Service) keyPair() (*rsa.PrivateKey, *x509.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.key != nil {
		return s.key, s.cert, nil
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("sp key: %w", err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "surifleet-saml-sp", Organization: []string{"SuriFleet"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, fmt.Errorf("sp cert: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, err
	}
	s.key = key
	s.cert = cert
	return s.key, s.cert, nil
}

// sp — собранный ServiceProvider для провайдера (кэшируется).
func (s *Service) sp(ctx context.Context, id uuid.UUID, cfg Config) (*saml.ServiceProvider, error) {
	s.mu.Lock()
	if sp, ok := s.sps[id]; ok {
		s.mu.Unlock()
		return sp, nil
	}
	s.mu.Unlock()

	key, cert, err := s.keyPair()
	if err != nil {
		return nil, err
	}
	md, err := s.idpMetadata(ctx, cfg)
	if err != nil {
		return nil, err
	}
	mdURL, _ := url.Parse(cfg.SPEntityID)
	acsURL, _ := url.Parse(cfg.ACSURL)
	sp := &saml.ServiceProvider{
		Key:               key,
		Certificate:       cert,
		MetadataURL:       *mdURL,
		AcsURL:            *acsURL,
		IDPMetadata:       md,
		AllowIDPInitiated: true,
	}
	s.mu.Lock()
	s.sps[id] = sp
	s.mu.Unlock()
	return sp, nil
}

// idpMetadata — метаданные IdP: inline XML или по URL (с кэшем в sp).
func (s *Service) idpMetadata(ctx context.Context, cfg Config) (*saml.EntityDescriptor, error) {
	var raw []byte
	if cfg.IDPMetadataXML != "" {
		raw = []byte(cfg.IDPMetadataXML)
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cfg.IDPMetadataURL, nil)
		if err != nil {
			return nil, err
		}
		resp, err := s.hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("загрузка метаданных IdP %s: %w", cfg.IDPMetadataURL, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("метаданные IdP: HTTP %d", resp.StatusCode)
		}
		buf := make([]byte, 0, 1<<20)
		tmp := make([]byte, 32<<10)
		for {
			n, err := resp.Body.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if err != nil || len(buf) > 2<<20 {
				break
			}
		}
		raw = buf
	}
	md := &saml.EntityDescriptor{}
	if err := xml.Unmarshal(raw, md); err != nil {
		return nil, fmt.Errorf("разбор метаданных IdP: %w", err)
	}
	return md, nil
}

// SP возвращает ServiceProvider для провайдера из БД (валидация конфига).
func (s *Service) SP(ctx context.Context, p store.SsoProvider) (*saml.ServiceProvider, Config, error) {
	cfg, err := ConfigFrom(p.ConfigRaw)
	if err != nil {
		return nil, cfg, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, cfg, err
	}
	sp, err := s.sp(ctx, p.ID, cfg)
	return sp, cfg, err
}
