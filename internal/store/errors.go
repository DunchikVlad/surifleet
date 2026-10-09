package store

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Доменные ошибки слоя БД. HTTP-слой отображает их на коды openapi Error.
var (
	// ErrNotFound — запись не найдена (GET/PUT/DELETE по id, 0 строк).
	ErrNotFound = errors.New("запись не найдена")
	// ErrConflict — нарушение уникальности (23505): slug, (org,name), (cluster,hostname).
	ErrConflict = errors.New("конфликт уникальности")
	// ErrForeignKey — нарушение внешнего ключа (23503): родитель не существует.
	ErrForeignKey = errors.New("родительская запись не существует")
	// ErrCycle — цикл в цепочке наследования (parent_id) профилей конфигурации.
	ErrCycle = errors.New("цикл в цепочке наследования")
)

// translate приводит ошибки pgx/PG к доменным ошибкам слоя.
func translate(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505": // unique_violation
			return wrapConflict(pgErr)
		case "23503": // foreign_key_violation
			return ErrForeignKey
		}
	}
	return err
}

// wrapConflict обогащает конфликт именем ограничения — по нему HTTP-слой
// определяет поле формы (slug / name / hostname).
func wrapConflict(pgErr *pgconn.PgError) error {
	return &ConflictError{Constraint: pgErr.ConstraintName}
}

// ConflictError — конфликт уникальности с именем ограничения.
type ConflictError struct {
	Constraint string
}

func (e *ConflictError) Error() string {
	return ErrConflict.Error() + ": " + e.Constraint
}

func (e *ConflictError) Unwrap() error { return ErrConflict }
