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
	return s, nil
}

// Close закрывает пул соединений (вызывать при graceful shutdown).
func (s *Store) Close() {
	if s != nil && s.Pool != nil {
		s.Pool.Close()
	}
}
