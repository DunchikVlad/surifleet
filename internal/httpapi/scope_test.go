package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

func TestClusterScopeAllowed(t *testing.T) {
	c1, c2, c3 := uuid.New(), uuid.New(), uuid.New()

	cases := []struct {
		name      string
		id        Identity
		clusterID uuid.UUID
		want      bool
	}{
		{"dev — всё разрешено", Identity{Dev: true, ScopeRestricted: true}, c3, true},
		{"org-scope (не restricted) — вся org", Identity{ScopeRestricted: false}, c3, true},
		{"restricted — кластер в scope", Identity{ScopeRestricted: true, ScopeClusters: []uuid.UUID{c1, c2}}, c1, true},
		{"restricted — другой кластер в scope", Identity{ScopeRestricted: true, ScopeClusters: []uuid.UUID{c1, c2}}, c2, true},
		{"restricted — кластер вне scope", Identity{ScopeRestricted: true, ScopeClusters: []uuid.UUID{c1, c2}}, c3, false},
		{"restricted — пустой scope (ничего не видит)", Identity{ScopeRestricted: true, ScopeClusters: []uuid.UUID{}}, c1, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.id.ClusterScopeAllowed(tc.clusterID); got != tc.want {
				t.Errorf("ClusterScopeAllowed = %v, ожидается %v", got, tc.want)
			}
		})
	}
}
