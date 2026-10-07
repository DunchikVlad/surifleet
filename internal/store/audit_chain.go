package store

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// ---------------------------------------------------------------------------
// Цепочка хэшей аудит-лога (чанк 38, п. 8 ТЗ: «защита от подделки —
// append-only, опционально цепочка хэшей записей»).
//
// Каждая запись: prev_hash = hash предыдущей записи, hash = SHA-256 над
// каноническим представлением (prev_hash + все поля записи, включая
// created_at — он задаётся в коде, а не DEFAULT now(), чтобы попасть в хэш).
// Запись вставляется под advisory-xact-блокировкой — цепочка не раздваивается
// даже при параллельных писателях (несколько API-процессов, одна БД).
// Включение: server.audit_hash_chain=true (AuditRepo.HashChain).
// ---------------------------------------------------------------------------

// auditChainLockID — ключ advisory-блокировки цепочки аудита (произвольная
// константа; одна на кластер БД).
const auditChainLockID int64 = 0x73757269666c6565 // "suriflee"

// auditHashVersion — версия канонической формы (в хэше; эволюция формы —
// новая версия, старые записи продолжают проверяться своей).
const auditHashVersion = "v1"

// canonStr — каноническая строка записи для хэширования: поля через \x1f
// (unit separator), nil → "". created_at — RFC3339Nano UTC, усечённый до
// МИКРОсекунд: timestamptz в PG хранит микросекунды, иначе запись (ns) и
// чтение (µs) дают разные канонические строки (живой баг чанка 47-48:
// verify ложно «подделка»).
func (e AuditEntry) canonStr(createdAt time.Time, prevHash string) string {
	s := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	u := func(p *uuid.UUID) string {
		if p == nil {
			return ""
		}
		return p.String()
	}
	fields := []string{
		auditHashVersion,
		prevHash,
		createdAt.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano),
		u(e.OrganizationID),
		e.ActorType, u(e.ActorUserID), u(e.ActorSessionID), u(e.ActorAPIKeyID),
		e.ActorName, s(e.IP), s(e.UserAgent),
		e.Action, s(e.ObjectType), u(e.ObjectID), s(e.ObjectName),
		e.Result, s(e.Reason),
		string(e.Diff),
	}
	return strings.Join(fields, "\x1f")
}

// computeAuditHash — SHA-256 канонической строки (base64url, как токены).
func computeAuditHash(e AuditEntry, createdAt time.Time, prevHash string) string {
	sum := sha256.Sum256([]byte(e.canonStr(createdAt, prevHash)))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// logChained — запись в цепочку: под advisory-блокировкой читается хэш
// последней записи, вычисляется hash новой, строка вставляется с явным
// created_at (он входит в хэш). Сериялизуется БД — вилки исключены.
func (r *AuditRepo) logChained(ctx context.Context, e AuditEntry) error {
	if e.ActorType == "" {
		e.ActorType = "user"
	}
	if e.Result == "" {
		e.Result = "success"
	}
	var ip pgtype.Text
	if e.IP != nil {
		ip = pgtype.Text{String: *e.IP, Valid: true}
	}
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return translate(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, auditChainLockID); err != nil {
		return translate(err)
	}
	// Хэш последней записи цепочки (первая запись — с пустым prev_hash).
	var prevHash string
	_ = tx.QueryRow(ctx,
		`SELECT COALESCE(hash, '') FROM audit_log
		 ORDER BY created_at DESC, id DESC LIMIT 1`).Scan(&prevHash)

	createdAt := time.Now().UTC()
	h := computeAuditHash(e, createdAt, prevHash)
	_, err = tx.Exec(ctx,
		`INSERT INTO audit_log (organization_id, actor_type, actor_user_id, actor_session_id,
		 actor_api_token_id, actor_name, ip, user_agent, action, object_type, object_id, object_name,
		 result, reason, diff, prev_hash, hash, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7::inet,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18)`,
		e.OrganizationID, e.ActorType, e.ActorUserID, e.ActorSessionID,
		e.ActorAPIKeyID, e.ActorName, ip, e.UserAgent, e.Action, e.ObjectType, e.ObjectID, e.ObjectName,
		e.Result, e.Reason, nullableJSON(e.Diff), prevHash, h, createdAt)
	if err != nil {
		return translate(err)
	}
	return translate(tx.Commit(ctx))
}

// auditChainRow — запись цепочки для проверки.
type auditChainRow struct {
	id        uuid.UUID
	createdAt time.Time
	prevHash  string
	hash      string
	entry     AuditEntry
}

// ChainVerifyResult — итог проверки цепочки хэшей (GET /audit_log/verify).
type ChainVerifyResult struct {
	Checked  int        `json:"checked"`             // записей проверено
	Chained  int        `json:"chained"`             // из них — в цепочке (hash не пуст)
	OK       bool       `json:"ok"`                  // вилок/расхождений не найдено
	BrokenAt *uuid.UUID `json:"broken_at,omitempty"` // id первой битой записи
	Reason   string     `json:"reason,omitempty"`
}

// VerifyChain проверяет последние limit записей цепочки (свежие первыми):
// 1) пересчёт hash из prev_hash + полей — подделка полей ломает совпадение;
// 2) связность prev_hash_i == hash_{i-1} — удаление/вставка ломает звено.
// Записи без hash (писались до включения цепочки) пропускаются в подсчёт
// Checked, но не в Chained. limit ≤ 100000.
func (r *AuditRepo) VerifyChain(ctx context.Context, limit int) (ChainVerifyResult, error) {
	if limit <= 0 {
		limit = 1000
	}
	if limit > 100000 {
		limit = 100000
	}
	rows, err := r.pool.Query(ctx,
		`SELECT id, created_at, organization_id, actor_type, actor_user_id, actor_session_id,
		 actor_api_token_id, actor_name, ip::text, user_agent, action, object_type, object_id,
		 object_name, result, reason, diff, COALESCE(prev_hash, ''), COALESCE(hash, '')
		 FROM audit_log ORDER BY created_at DESC, id DESC LIMIT $1`, limit)
	if err != nil {
		return ChainVerifyResult{}, translate(err)
	}
	defer rows.Close()

	chain := []auditChainRow{}
	for rows.Next() {
		var cr auditChainRow
		var e AuditEntry
		var actorName, ip, ua, objType, objName, reason *string
		var orgID, actorUID, sessID, apiKeyID, objID *uuid.UUID
		var diff json.RawMessage
		if err := rows.Scan(&cr.id, &cr.createdAt, &orgID, &e.ActorType, &actorUID, &sessID,
			&apiKeyID, &actorName, &ip, &ua, &e.Action, &objType, &objID,
			&objName, &e.Result, &reason, &diff, &cr.prevHash, &cr.hash); err != nil {
			return ChainVerifyResult{}, translate(err)
		}
		if actorName != nil {
			e.ActorName = *actorName
		}
		// ip — inet: PG отдаёт CIDR-нотацию ("192.168.31.50/32"), а в канон
		// при записи шёл чистый адрес — нормализуем к хосту (без /маски).
		if ip != nil {
			host, _, _ := strings.Cut(*ip, "/")
			e.IP = &host
		}
		e.OrganizationID, e.ActorUserID, e.ActorSessionID, e.ActorAPIKeyID = orgID, actorUID, sessID, apiKeyID
		e.UserAgent, e.ObjectType, e.ObjectID, e.ObjectName, e.Reason, e.Diff = ua, objType, objID, objName, reason, diff
		cr.entry = e
		chain = append(chain, cr)
	}
	if err := rows.Err(); err != nil {
		return ChainVerifyResult{}, translate(err)
	}

	res := ChainVerifyResult{OK: true, Checked: len(chain)}
	// Проверка от новых к старым: у записи i (новее j) prev_hash_i == hash_j.
	for i, cr := range chain {
		if cr.hash == "" {
			continue // доцепочечная запись — проверять нечего
		}
		res.Chained++
		if got := computeAuditHash(cr.entry, cr.createdAt, cr.prevHash); got != cr.hash {
			res.OK = false
			id := cr.id
			res.BrokenAt = &id
			res.Reason = "hash записи не совпадает с содержимым (подделка полей?)"
			return res, nil
		}
		// Связность со следующей (более старой) записью в цепочке.
		if i+1 < len(chain) && chain[i+1].hash != "" && cr.prevHash != chain[i+1].hash {
			res.OK = false
			id := cr.id
			res.BrokenAt = &id
			res.Reason = "разрыв цепочки: prev_hash не совпадает с hash предыдущей записи (удаление/вставка?)"
			return res, nil
		}
	}
	return res, nil
}

// errChainDisabled — VerifyChain при выключенной цепочке (информативно).
var errChainDisabled = errors.New("цепочка хэшей выключена (server.audit_hash_chain=false)")

// VerifyChainEnabled — проверка с явной ошибкой, если цепочка выключена.
func (r *AuditRepo) VerifyChainEnabled(ctx context.Context, limit int) (ChainVerifyResult, error) {
	if !r.HashChain {
		return ChainVerifyResult{OK: true, Reason: errChainDisabled.Error()}, nil
	}
	return r.VerifyChain(ctx, limit)
}
