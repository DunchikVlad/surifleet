package store

import (
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
)

// migrationsFS — встроенные в бинарь SQL-миграции.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// ErrMigrateDirty возвращается, если база находится в dirty-состоянии
// (предыдущая миграция упала посередине — нужен ручной force).
var ErrMigrateDirty = errors.New("база в dirty-состоянии: требуется migrate force")

// Migrate применяет все неприменённые миграции (Up). Отсутствие изменений
// (migrate.ErrNoChange) — нормальная ситуация, ошибкой не считается.
// Возвращает применённую версию и признак «изменений не было».
func Migrate(pool *pgxpool.Pool) (version uint, noChange bool, err error) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return 0, false, fmt.Errorf("источник миграций: %w", err)
	}

	// golang-migrate работает через database/sql — открываем stdlib-адаптер
	// поверх той же конфигурации соединения (отдельное соединение на время
	// миграций, пул не блокируем).
	db := stdlib.OpenDB(*pool.Config().ConnConfig)
	defer func() { _ = db.Close() }()

	driver, err := postgres.WithInstance(db, &postgres.Config{})
	if err != nil {
		return 0, false, fmt.Errorf("драйвер миграций postgres: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "postgres", driver)
	if err != nil {
		return 0, false, fmt.Errorf("инициализация мигратора: %w", err)
	}

	upErr := m.Up()
	version, dirty, vErr := m.Version()
	switch {
	case errors.Is(upErr, migrate.ErrNoChange):
		return version, true, nil
	case upErr != nil:
		if dirty {
			return version, false, fmt.Errorf("%w (версия %d): %v", ErrMigrateDirty, version, upErr)
		}
		return version, false, fmt.Errorf("применение миграций: %w", upErr)
	case vErr != nil && !errors.Is(vErr, migrate.ErrNilVersion):
		return 0, false, fmt.Errorf("версия миграций: %w", vErr)
	}
	return version, false, nil
}
