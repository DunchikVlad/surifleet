// Package compliance — расчёт статуса соответствия инстанса (ТЗ п.6):
// in_sync / pending / partial / drift / stale по desired/actual state и
// факту онлайна агента. Чистая функция — инкрементальный пересчёт по
// событиям (отчёт агента, смена desired, connect/disconnect) делается
// вызывающей стороной (hub/оркестратор), здесь — только логика.
package compliance

// Статусы соответствия (CHECK в DDL instance_compliance + enum openapi).
const (
	InSync  = "in_sync"
	Pending = "pending"
	Partial = "partial"
	Drift   = "drift"
	Stale   = "stale"
)

// Desired — целевое состояние инстанса (из desired_state).
type Desired struct {
	RulesetHash string
	RuleSids    []int64 // рассчитанный набор (computed_rules → sids)
}

// Actual — фактическое состояние (последний отчёт агента).
type Actual struct {
	RulesetHash string
	LoadedSids  []int64
	FailedRules []FailedRule
}

// FailedRule — правило, отклонённое движком, с текстом ошибки.
type FailedRule struct {
	Sid       int64  `json:"sid"`
	Rev       int32  `json:"rev"`
	ErrorText string `json:"error_text"`
}

// Details — содержимое instance_compliance.details (diff/failed).
type Details struct {
	MissingRules []int64      `json:"missing_rules,omitempty"` // должны быть, но не загружены
	ExtraRules   []int64      `json:"extra_rules,omitempty"`   // загружены вне desired
	FailedRules  []FailedRule `json:"failed_rules,omitempty"`
	Reason       string       `json:"reason,omitempty"`
}

// Compute рассчитывает статус соответствия и details:
//   - агент офлайн → stale (данным нельзя доверять);
//   - desired есть, отчёта нет → pending;
//   - хэши совпали, failed=0 → in_sync;
//   - хэши совпали, failed>0 → partial (со списком failed);
//   - хэши различаются → drift (diff missing/extra по sid).
func Compute(desired *Desired, actual *Actual, agentOnline bool) (string, Details) {
	if !agentOnline {
		return Stale, Details{Reason: "агент офлайн: данные недостоверны"}
	}
	if desired == nil {
		// Целевого нет — нечему соответствовать; считаем pending.
		return Pending, Details{Reason: "desired state не задан (деплоев не было)"}
	}
	if actual == nil {
		return Pending, Details{Reason: "отчёт агента ещё не получен"}
	}
	if actual.RulesetHash == desired.RulesetHash {
		if len(actual.FailedRules) == 0 {
			return InSync, Details{}
		}
		return Partial, Details{FailedRules: actual.FailedRules}
	}
	return Drift, Details{
		MissingRules: diffSids(desired.RuleSids, actual.LoadedSids),
		ExtraRules:   diffSids(actual.LoadedSids, desired.RuleSids),
		FailedRules:  actual.FailedRules,
	}
}

// diffSids — элементы a, которых нет в b (упорядоченно, без дублей).
func diffSids(a, b []int64) []int64 {
	inB := make(map[int64]bool, len(b))
	for _, s := range b {
		inB[s] = true
	}
	seen := map[int64]bool{}
	var out []int64
	for _, s := range a {
		if !inB[s] && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
