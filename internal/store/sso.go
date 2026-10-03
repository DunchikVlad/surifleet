package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ---------------------------------------------------------------------------
// SSO-провайдеры и OIDC-состояния (чанк 35 — OIDC-SSO, п. 9 ТЗ)
// ---------------------------------------------------------------------------

// OIDCConfig — конфигурация OIDC-провайдера (sso_providers.config jsonb).
// client_secret — секрет (writeOnly в API: принимается, наружу не отдаётся).
type OIDCConfig struct {
	IssuerURL    string   `json:"issuer_url"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret,omitempty"`
	RedirectURL  string   `json:"redirect_url"`
	Scopes       []string `json:"scopes,omitempty"`
}

// SsoProvider — SSO-провайдер организации (sso_providers).
// Config.ClientSecret скрывается в JSON наружу (см. SsoProvider.Public).
type SsoProvider struct {
	ID             uuid.UUID  `json:"id"`
	OrganizationID uuid.UUID  `json:"organization_id"`
	Name           string     `json:"name"`
	Type           string     `json:"type"` // oidc | saml | ldap
	Config         OIDCConfig `json:"config"`
	// ConfigRaw — сырое jsonb-поле config (для не-OIDC типов: ldap/saml,
	// у которых своя схема конфигурации). В JSON наружу не отдаётся.
	ConfigRaw        json.RawMessage `json:"-"`
	GroupRoleMapping json.RawMessage `json:"group_role_mapping"`
	Enabled          bool            `json:"enabled"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

// Public — представление провайдера без секрета (client_secret обнуляется).
func (p SsoProvider) Public() SsoProvider {
	p.Config.ClientSecret = ""
	return p
}

// SsoProviderInput — создание/обновление провайдера.
// Config — типизированный OIDC-конфиг; ConfigRaw — сырой JSON (ldap/saml).
// При записи в БД приоритет у ConfigRaw (если задан).
type SsoProviderInput struct {
	Name             string
	Type             string
	Config           OIDCConfig
	ConfigRaw        json.RawMessage // сырой jsonb config (ldap/saml); приоритетнее Config
	GroupRoleMapping json.RawMessage // nil — не менять (PATCH) / '{}' (POST)
	Enabled          *bool           // nil — не менять
}

// configJSON — итоговый jsonb config: ConfigRaw, иначе маршалинг Config.
func (in SsoProviderInput) configJSON() ([]byte, error) {
	if len(in.ConfigRaw) > 0 {
		return in.ConfigRaw, nil
	}
	return json.Marshal(in.Config)
}

// SsoProvidersRepo — CRUD sso_providers.
type SsoProvidersRepo struct {
	pool *pgxpool.Pool
}

const ssoProviderColumns = `id, organization_id, name, type, config, group_role_mapping, enabled, created_at, updated_at`

func scanSsoProvider(row pgx.Row) (SsoProvider, error) {
	var p SsoProvider
	var cfg []byte
	err := row.Scan(&p.ID, &p.OrganizationID, &p.Name, &p.Type, &cfg,
		&p.GroupRoleMapping, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return p, translate(err)
	}
	if len(cfg) > 0 {
		p.ConfigRaw = json.RawMessage(cfg)
		if err := json.Unmarshal(cfg, &p.Config); err != nil {
			return p, err
		}
	}
	if len(p.GroupRoleMapping) == 0 {
		p.GroupRoleMapping = json.RawMessage("{}")
	}
	return p, nil
}

// List — провайдеры организации (client_secret присутствует — слой API
// обязан отдавать Public()).
func (r *SsoProvidersRepo) List(ctx context.Context, orgID uuid.UUID) ([]SsoProvider, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+ssoProviderColumns+` FROM sso_providers WHERE organization_id = $1 ORDER BY name`, orgID)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []SsoProvider{}
	for rows.Next() {
		p, err := scanSsoProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, translate(rows.Err())
}

// GetByID — провайдер по id (org не проверяется — scoping на API-слое).
func (r *SsoProvidersRepo) GetByID(ctx context.Context, id uuid.UUID) (SsoProvider, error) {
	return scanSsoProvider(r.pool.QueryRow(ctx,
		`SELECT `+ssoProviderColumns+` FROM sso_providers WHERE id = $1`, id))
}

// Create создаёт провайдера. Дубль (org,name) → ErrConflict.
func (r *SsoProvidersRepo) Create(ctx context.Context, orgID uuid.UUID, in SsoProviderInput) (SsoProvider, error) {
	cfg, err := in.configJSON()
	if err != nil {
		return SsoProvider{}, err
	}
	grm := in.GroupRoleMapping
	if len(grm) == 0 {
		grm = json.RawMessage("{}")
	}
	enabled := true
	if in.Enabled != nil {
		enabled = *in.Enabled
	}
	return scanSsoProvider(r.pool.QueryRow(ctx,
		`INSERT INTO sso_providers (organization_id, name, type, config, group_role_mapping, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 RETURNING `+ssoProviderColumns,
		orgID, in.Name, in.Type, cfg, grm, enabled))
}

// Update — частичное обновление провайдера; пустые поля in — «не менять»
// (Config нулевая → config не трогаем; GroupRoleMapping nil — не трогаем;
// Enabled nil — не трогаем). Нет записи → ErrNotFound.
func (r *SsoProvidersRepo) Update(ctx context.Context, id uuid.UUID, in SsoProviderInput) (SsoProvider, error) {
	cur, err := r.GetByID(ctx, id)
	if err != nil {
		return SsoProvider{}, err
	}
	if in.Name != "" {
		cur.Name = in.Name
	}
	if in.Type != "" {
		cur.Type = in.Type
	}
	// Config: приоритет ConfigRaw (ldap/saml — пишем jsonb как есть).
	// Иначе OIDC: обновляем только осмысленную (issuer_url не пуст);
	// пустой client_secret в PATCH — «не менять» (сохраняем старый).
	var cfg []byte
	if len(in.ConfigRaw) > 0 {
		cfg = in.ConfigRaw
		cur.ConfigRaw = in.ConfigRaw
	} else {
		if in.Config.IssuerURL != "" || in.Config.ClientID != "" || in.Config.RedirectURL != "" {
			if in.Config.ClientSecret == "" {
				in.Config.ClientSecret = cur.Config.ClientSecret
			}
			cur.Config = in.Config
		}
		var err error
		cfg, err = json.Marshal(cur.Config)
		if err != nil {
			return SsoProvider{}, err
		}
	}
	if len(in.GroupRoleMapping) > 0 {
		cur.GroupRoleMapping = in.GroupRoleMapping
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	return scanSsoProvider(r.pool.QueryRow(ctx,
		`UPDATE sso_providers SET name=$2, type=$3, config=$4, group_role_mapping=$5,
			enabled=$6, updated_at=now()
		 WHERE id=$1 RETURNING `+ssoProviderColumns,
		id, cur.Name, cur.Type, cfg, cur.GroupRoleMapping, cur.Enabled))
}

// Delete удаляет провайдера (JIT-пользователи сохраняются — provider_id → NULL
// по FK ON DELETE SET NULL; войти через SSO больше не смогут). Нет записи →
// ErrNotFound.
func (r *SsoProvidersRepo) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM sso_providers WHERE id = $1`, id)
	if err != nil {
		return translate(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ListEnabledOIDC — включённые OIDC-провайдеры (для публичного списка
// на форме входа). Без client_secret — только id/name для кнопки.
func (r *SsoProvidersRepo) ListEnabledOIDC(ctx context.Context) ([]SsoProvider, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT `+ssoProviderColumns+` FROM sso_providers WHERE type='oidc' AND enabled ORDER BY name`)
	if err != nil {
		return nil, translate(err)
	}
	defer rows.Close()
	out := []SsoProvider{}
	for rows.Next() {
		p, err := scanSsoProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, translate(rows.Err())
}

// ---------------------------------------------------------------------------
// OIDC state/nonce (одноразовые, защита от CSRF/replay)
// ---------------------------------------------------------------------------

// OidcStatesRepo — одноразовые state+nonce OIDC-flow (таблица oidc_states).
type OidcStatesRepo struct {
	pool *pgxpool.Pool
}

// Create сохраняет state+nonce для провайдера с TTL.
func (r *OidcStatesRepo) Create(ctx context.Context, providerID uuid.UUID, state, nonce string, ttl time.Duration) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO oidc_states (state, provider_id, nonce, expires_at) VALUES ($1,$2,$3,now()+$4::interval)`,
		state, providerID, nonce, ttl.String())
	return translate(err)
}

// Consume атомарно забирает state (DELETE … RETURNING): повторное
// использование того же state → ErrNotFound (защита от replay). Истёкший —
// тоже ErrNotFound.
func (r *OidcStatesRepo) Consume(ctx context.Context, state string) (providerID uuid.UUID, nonce string, err error) {
	err = r.pool.QueryRow(ctx,
		`DELETE FROM oidc_states WHERE state=$1 AND expires_at > now() RETURNING provider_id, nonce`, state).
		Scan(&providerID, &nonce)
	if err != nil {
		return uuid.Nil, "", translate(err)
	}
	return providerID, nonce, nil
}

// SweepExpired удаляет протухшие state (фоновая очистка).
func (r *OidcStatesRepo) SweepExpired(ctx context.Context) (int64, error) {
	tag, err := r.pool.Exec(ctx, `DELETE FROM oidc_states WHERE expires_at <= now()`)
	if err != nil {
		return 0, translate(err)
	}
	return tag.RowsAffected(), nil
}
