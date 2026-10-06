package httpapi

import (
	"testing"

	"github.com/google/uuid"
)

func TestIntersectIDs(t *testing.T) {
	a, b, c, d := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	cases := []struct {
		name    string
		ids     []uuid.UUID
		allowed []uuid.UUID
		want    int
	}{
		{"все разрешены", []uuid.UUID{a, b}, []uuid.UUID{a, b, c}, 2},
		{"часть отфильтрована", []uuid.UUID{a, b, c}, []uuid.UUID{a, c}, 2},
		{"ничего не разрешено", []uuid.UUID{a, b}, []uuid.UUID{c, d}, 0},
		{"пустые цели", []uuid.UUID{}, []uuid.UUID{a}, 0},
		{"пустое разрешение", []uuid.UUID{a}, []uuid.UUID{}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := intersectIDs(tc.ids, tc.allowed)
			if len(got) != tc.want {
				t.Errorf("intersectIDs = %d, ожидается %d", len(got), tc.want)
			}
		})
	}
	// Порядок сохраняется.
	got := intersectIDs([]uuid.UUID{c, a, b}, []uuid.UUID{a, b, c})
	if len(got) != 3 || got[0] != c || got[1] != a || got[2] != b {
		t.Errorf("порядок = %v", got)
	}
}
