// Package store — слой доступа к PostgreSQL SuriFleet: пул соединений
// pgx/v5, встроенные миграции (golang-migrate, источник iofs из embed.FS)
// и репозитории сущностей флота (organizations, clusters, hosts).
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store — корневой объект слоя БД: пул соединений и репозитории.
type Store struct {
	Pool *pgxpool.Pool

	Organizations *OrganizationsRepo
	Clusters      *ClustersRepo
	Hosts         *HostsRepo
	JoinTokens    *JoinTokensRepo
	Agents        *AgentsRepo
	Instances     *InstancesRepo
	Rules         *RulesRepo
	Iocs          *IocsRepo
	Feeds         *FeedsRepo
	Rulesets      *RulesetsRepo
	Deployments   *DeploymentsRepo
	DesiredState  *DesiredStateRepo
	ActualState   *ActualStateRepo
	Compliance    *ComplianceRepo
	Capabilities  *CapabilitiesRepo
	Users         *UsersRepo
	Roles         *RolesRepo
	Sessions      *SessionsRepo
	Audit         *AuditRepo
	ApiTokens     *ApiTokensRepo
	Configs       *ConfigsRepo
	ConfigProfiles *ConfigProfilesRepo
	SsoProviders  *SsoProvidersRepo
	OidcStates    *OidcStatesRepo
}

// Connect открывает пул соединений по DSN и проверяет его ping'ом.
// DSN формата postgres://user:pass@host:5432/dbname?sslmode=disable.
func Connect(ctx context.Context, dsn string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("разбор postgres_dsn: %w", err)
	}
	// Консервативные дефолты пула для тестового контура.
	if cfg.MaxConns == 0 {
		cfg.MaxConns = 10
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("подключение к PostgreSQL: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}

	s := &Store{Pool: pool}
	s.Organizations = &OrganizationsRepo{pool: pool}
	s.Clusters = &ClustersRepo{pool: pool}
	s.Hosts = &HostsRepo{pool: pool}
	s.JoinTokens = &JoinTokensRepo{pool: pool}
	s.Agents = &AgentsRepo{pool: pool}
	s.Instances = &InstancesRepo{pool: pool}
	s.Rules = &RulesRepo{pool: pool}
	s.Iocs = &IocsRepo{pool: pool}
	s.Feeds = &FeedsRepo{pool: pool}
	s.Rulesets = &RulesetsRepo{pool: pool}
	s.Deployments = &DeploymentsRepo{pool: pool}
	s.DesiredState = &DesiredStateRepo{pool: pool}
	s.ActualState = &ActualStateRepo{pool: pool}
	s.Compliance = &ComplianceRepo{pool: pool}
	s.Capabilities = &CapabilitiesRepo{pool: pool}
	s.Users = &UsersRepo{pool: pool}
	s.Roles = &RolesRepo{pool: pool}
	s.Sessions = &SessionsRepo{pool: pool}
	s.Audit = &AuditRepo{pool: pool}
	s.ApiTokens = &ApiTokensRepo{pool: pool}
	s.Configs = &ConfigsRepo{pool: pool}
	s.ConfigProfiles = &ConfigProfilesRepo{pool: pool}
	s.SsoProviders = &SsoProvidersRepo{pool: pool}
	s.OidcStates = &OidcStatesRepo{pool: pool}
	return s, nil
}

// Close закрывает пул соединений (вызывать при graceful shutdown).
func (s *Store) Close() {
	if s != nil && s.Pool != nil {
		s.Pool.Close()
	}
}
