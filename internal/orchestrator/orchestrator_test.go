package orchestrator

import (
	"testing"

	"github.com/google/uuid"
)

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func waveOf(waves []struct {
	InstanceID uuid.UUID
	Wave       int
}, id uuid.UUID) int {
	for _, w := range waves {
		if w.InstanceID == id {
			return w.Wave
		}
	}
	return -1
}

func TestComputeWaves_NoCanary(t *testing.T) {
	in := ids(5)
	waves := ComputeWaves(in, 0, 2)
	if len(waves) != 5 {
		t.Fatalf("ожидалось 5 задач, получено %d", len(waves))
	}
	counts := map[int]int{}
	for _, w := range waves {
		counts[w.Wave]++
	}
	// 5 инстансов батчами по 2: волны 0(2), 1(2), 2(1).
	if counts[0] != 2 || counts[1] != 2 || counts[2] != 1 {
		t.Fatalf("неверная раскладка без canary: %v", counts)
	}
}

func TestComputeWaves_Canary(t *testing.T) {
	in := ids(6)
	waves := ComputeWaves(in, 1, 2)
	counts := map[int]int{}
	for _, w := range waves {
		counts[w.Wave]++
	}
	// canary: волна 0 (1 инстанс); остальные 5 батчами по 2: волны 1(2), 2(2), 3(1).
	if counts[0] != 1 || counts[1] != 2 || counts[2] != 2 || counts[3] != 1 {
		t.Fatalf("неверная раскладка с canary: %v", counts)
	}
}

func TestComputeWaves_CanaryCoversAll(t *testing.T) {
	in := ids(2)
	waves := ComputeWaves(in, 5, 2)
	for _, w := range waves {
		if w.Wave != 0 {
			t.Fatalf("canary >= fleet: все должны быть в волне 0, получено %d", w.Wave)
		}
	}
}

func TestComputeWaves_Deterministic(t *testing.T) {
	in := ids(10)
	a := ComputeWaves(in, 2, 3)
	b := ComputeWaves(in, 2, 3)
	if len(a) != len(b) {
		t.Fatal("разная длина")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("недетерминированная раскладка на шаге %d", i)
		}
	}
}

func TestComputeWaves_Empty(t *testing.T) {
	if w := ComputeWaves(nil, 1, 50); len(w) != 0 {
		t.Fatalf("пустой флот → пустые волны, получено %d", len(w))
	}
}
