package main

import (
	"os"
	"path/filepath"
	"testing"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

const eveSample = `{"timestamp":"2026-10-02T18:00:00.000Z","event_type":"flow","src_ip":"1.2.3.4"}
{"timestamp":"2026-10-02T18:00:08.000Z","event_type":"stats","stats":{"uptime":100,"capture":{"kernel_packets":17674,"kernel_drops":5},"decoder":{"pkts":17673,"bytes":3255374},"flow":{"memuse":7234567},"detect":{"alert":42}}}
`

// TestEveTailer — инкрементальное чтение stats-событий, извлечение метрик,
// устойчивость к усечению файла (ротация).
func TestEveTailer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "eve.json")
	if err := os.WriteFile(path, []byte(eveSample), 0o644); err != nil {
		t.Fatal(err)
	}

	tl := &eveTailer{path: path}
	pts := tl.points("")
	if len(pts) != len(eveMetrics) {
		t.Fatalf("точек: %d, ожидалось %d (%+v)", len(pts), len(eveMetrics), pts)
	}
	got := map[string]float64{}
	for _, p := range pts {
		got[p.GetName()] = p.GetValue()
	}
	want := map[string]float64{
		"suricata.uptime_seconds":         100,
		"suricata.capture_kernel_packets": 17674,
		"suricata.capture_kernel_drops":   5,
		"suricata.decoder_pkts":           17673,
		"suricata.decoder_bytes":          3255374,
		"suricata.flow_memuse_bytes":      7234567,
		"suricata.detect_alert":           42,
	}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("%s: %v, ожидалось %v", name, got[name], v)
		}
	}

	// Повтор без новых данных — пусто.
	if pts := tl.points(""); pts != nil {
		t.Errorf("без новых данных: %+v, ожидалось nil", pts)
	}

	// Дописали ещё событие — пришло только оно.
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	_, _ = f.WriteString(`{"timestamp":"2026-10-02T18:01:08.000Z","event_type":"stats","stats":{"uptime":160,"capture":{"kernel_packets":18000,"kernel_drops":5},"decoder":{"pkts":17999,"bytes":3300000},"flow":{"memuse":8000000},"detect":{"alert":43}}}` + "\n")
	_ = f.Close()
	pts = tl.points("inst-1")
	if len(pts) == 0 || pts[0].GetInstanceId() != "inst-1" {
		t.Fatalf("после дописки: %+v", pts)
	}
	if pts[0].GetValue() != 160 {
		t.Errorf("uptime после дописки: %v, ожидалось 160", pts[0].GetValue())
	}

	// Усечение (ротация): offset сбрасывается, паники нет.
	if err := os.WriteFile(path, []byte(eveSample), 0o644); err != nil {
		t.Fatal(err)
	}
	if pts := tl.points(""); len(pts) == 0 {
		t.Error("после усечения: ожидались точки")
	}
}

// TestSuricataInstanceID — сопоставление log_dir с привязками HelloAck.
func TestSuricataInstanceID(t *testing.T) {
	bindings := []*agentv1.InstanceBinding{
		{InstanceId: "id-1", LogDir: "/var/log/suricata"},
		{InstanceId: "id-2", LogDir: "/var/log/suri2/"},
	}
	if got := suricataInstanceID(bindings, "/var/log/suricata/"); got != "id-1" {
		t.Errorf("trailing slash: %q, ожидалось id-1", got)
	}
	if got := suricataInstanceID(bindings, "/var/log/suri2"); got != "id-2" {
		t.Errorf("без slash: %q, ожидалось id-2", got)
	}
	if got := suricataInstanceID(bindings, "/other"); got != "" {
		t.Errorf("нет совпадения: %q, ожидалось пусто", got)
	}
}
