// evemetrics.go — метрики Suricata из eve.json (чанк 34): tail файла с
// отслеживанием смещения, разбор stats-событий (event_type=stats) и
// извлечение ключевых счётчиков в MetricPoint.
// eve.json пишется Suricata с заданной периодикой stats; агент раз в
// metrics-интервал читает только новые байты. Устойчив к ротации:
// уменьшение размера файла — читаем с нуля (пропуская историю).
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// eveMaxRead — предел чтения за тик (защита от гигантских eve.json на
// нагруженных сенсорах; хвост обрезается, статистика не теряется —
// берётся последнее stats-событие из прочитанного окна).
const eveMaxRead = 16 << 20

// eveTailer — чтение eve.json с отслеживанием смещения.
type eveTailer struct {
	path   string
	offset int64
}

// eveMetric — извлекаемый счётчик: путь внутри stats-события → имя метрики.
var eveMetrics = []struct {
	path []string
	name string
}{
	{[]string{"uptime"}, "suricata.uptime_seconds"},
	{[]string{"capture", "kernel_packets"}, "suricata.capture_kernel_packets"},
	{[]string{"capture", "kernel_drops"}, "suricata.capture_kernel_drops"},
	{[]string{"decoder", "pkts"}, "suricata.decoder_pkts"},
	{[]string{"decoder", "bytes"}, "suricata.decoder_bytes"},
	{[]string{"flow", "memuse"}, "suricata.flow_memuse_bytes"},
	{[]string{"detect", "alert"}, "suricata.detect_alert"},
}

// statsEvent — stats-событие eve.json (нужная часть).
type statsEvent struct {
	EventType string         `json:"event_type"`
	Stats     map[string]any `json:"stats"`
}

// lastStats читает новые байты eve.json и возвращает stats последнего
// stats-события из них (nil — новых событий не было или файл недоступен).
func (t *eveTailer) lastStats() map[string]any {
	st, err := os.Stat(t.path)
	if err != nil {
		return nil
	}
	if st.Size() < t.offset {
		t.offset = 0 // ротация/усечение — начинаем сначала
	}
	if st.Size() == t.offset {
		return nil
	}
	f, err := os.Open(t.path)
	if err != nil {
		return nil
	}
	defer f.Close()
	if _, err := f.Seek(t.offset, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, eveMaxRead))
	if err != nil {
		return nil
	}
	t.offset += int64(len(data))

	var last map[string]any
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 256<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"event_type":"stats"`)) {
			continue
		}
		var ev statsEvent
		if json.Unmarshal(line, &ev) == nil && ev.EventType == "stats" {
			last = ev.Stats
		}
	}
	return last
}

// points извлекает метрики из stats-события. instanceID — серверный id
// инстанса (пусто — не сопоставлен). nil, если stats нет.
func (t *eveTailer) points(instanceID string) []*agentv1.MetricPoint {
	stats := t.lastStats()
	if stats == nil {
		return nil
	}
	now := timestamppb.Now()
	var out []*agentv1.MetricPoint
	for _, m := range eveMetrics {
		v, ok := nestedFloat(stats, m.path...)
		if !ok {
			continue
		}
		out = append(out, &agentv1.MetricPoint{
			Ts: now, InstanceId: instanceID, Name: m.name, Value: v,
		})
	}
	return out
}

// nestedFloat — число по пути во вложенных map[string]any.
func nestedFloat(m map[string]any, path ...string) (float64, bool) {
	cur := m
	for i, key := range path {
		v, ok := cur[key]
		if !ok {
			return 0, false
		}
		if i == len(path)-1 {
			switch n := v.(type) {
			case float64:
				return n, true
			case json.Number:
				f, err := n.Float64()
				return f, err == nil
			}
			return 0, false
		}
		next, ok := v.(map[string]any)
		if !ok {
			return 0, false
		}
		cur = next
	}
	return 0, false
}

// suricataInstanceID — instance_id привязанного инстанса по log_dir
// (bound_instances из HelloAck); пусто, если совпадения нет.
func suricataInstanceID(bindings []*agentv1.InstanceBinding, logDir string) string {
	norm := strings.TrimRight(logDir, "/")
	for _, b := range bindings {
		if strings.TrimRight(b.GetLogDir(), "/") == norm && norm != "" {
			return b.GetInstanceId()
		}
	}
	return ""
}
