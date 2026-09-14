package compliance

import (
	"reflect"
	"testing"
)

func TestCompute(t *testing.T) {
	desired := &Desired{RulesetHash: "aaa", RuleSids: []int64{1, 2, 3}}
	cases := []struct {
		name        string
		desired     *Desired
		actual      *Actual
		online      bool
		wantStatus  string
		wantMissing []int64
		wantExtra   []int64
	}{
		{"in_sync: хэш совпал, failed=0", desired,
			&Actual{RulesetHash: "aaa", LoadedSids: []int64{1, 2, 3}}, true, InSync, nil, nil},
		{"partial: хэш совпал, failed>0", desired,
			&Actual{RulesetHash: "aaa", LoadedSids: []int64{1, 2}, FailedRules: []FailedRule{{Sid: 3, ErrorText: "bad"}}},
			true, Partial, nil, nil},
		{"drift: хэш не совпал → diff", desired,
			&Actual{RulesetHash: "bbb", LoadedSids: []int64{2, 3, 4}}, true, Drift, []int64{1}, []int64{4}},
		{"pending: отчёта нет", desired, nil, true, Pending, nil, nil},
		{"pending: desired не задан", nil, nil, true, Pending, nil, nil},
		{"stale: агент офлайн", desired,
			&Actual{RulesetHash: "aaa", LoadedSids: []int64{1, 2, 3}}, false, Stale, nil, nil},
		{"stale приоритетнее drift", desired,
			&Actual{RulesetHash: "zzz"}, false, Stale, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			status, det := Compute(c.desired, c.actual, c.online)
			if status != c.wantStatus {
				t.Errorf("status = %q, ожидалось %q", status, c.wantStatus)
			}
			if !reflect.DeepEqual(det.MissingRules, c.wantMissing) {
				t.Errorf("missing = %v, ожидалось %v", det.MissingRules, c.wantMissing)
			}
			if !reflect.DeepEqual(det.ExtraRules, c.wantExtra) {
				t.Errorf("extra = %v, ожидалось %v", det.ExtraRules, c.wantExtra)
			}
		})
	}
}

func TestDiffSidsDedup(t *testing.T) {
	got := diffSids([]int64{5, 1, 5, 3}, []int64{3})
	if !reflect.DeepEqual(got, []int64{5, 1}) {
		t.Errorf("diffSids = %v, ожидалось [5 1]", got)
	}
	if got := diffSids(nil, []int64{1}); got != nil {
		t.Errorf("diffSids(nil, [1]) = %v, ожидалось nil", got)
	}
}
