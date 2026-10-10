package autoruleset

import (
	"testing"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

// TestSuriupdateResultImported — гейт триггера пересборки (чанк 110, KI-6):
// list-only вызовы (GET /sources) не должны пересобирать авто-ruleset'ы.
func TestSuriupdateResultImported(t *testing.T) {
	suRes := func(uploaded int64) *agentv1.TaskResult {
		return &agentv1.TaskResult{
			Status: agentv1.TaskStatus_TASK_STATUS_SUCCESS,
			Details: &agentv1.TaskResult_SuricataUpdate{SuricataUpdate: &agentv1.SuricataUpdateResult{
				UploadedBytes: uploaded,
			}},
		}
	}
	cases := []struct {
		name string
		res  *agentv1.TaskResult
		want bool
	}{
		{"реальное обновление с заливкой", suRes(45883831), true},
		{"list-only (GET /sources)", suRes(0), false},
		{"не suricata-update вовсе", &agentv1.TaskResult{
			Status: agentv1.TaskStatus_TASK_STATUS_SUCCESS,
		}, false},
		{"failed с заливкой не бывает, но гейтим и статус", &agentv1.TaskResult{
			Status: agentv1.TaskStatus_TASK_STATUS_FAILED,
			Details: &agentv1.TaskResult_SuricataUpdate{SuricataUpdate: &agentv1.SuricataUpdateResult{
				UploadedBytes: 100,
			}},
		}, false},
	}
	for _, c := range cases {
		if got := suriupdateResultImported(c.res); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
