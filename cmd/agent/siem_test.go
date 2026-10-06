package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func alertJSON(sid int, sig, src string) string {
	return `{"timestamp":"2026-10-03T12:00:00.000000+0000","event_type":"alert","src_ip":"` + src + `","src_port":12345,"dest_ip":"10.0.0.1","dest_port":80,"proto":"TCP","alert":{"signature_id":` + strconv.Itoa(sid) + `,"signature":"` + sig + `","severity":1}}`
}

func TestToCEF(t *testing.T) {
	line := `{"timestamp":"2026-10-03T12:00:00.000000+0000","event_type":"alert","src_ip":"192.0.2.5","src_port":51000,"dest_ip":"198.51.100.9","dest_port":443,"proto":"TCP","alert":{"signature_id":2051001,"signature":"ET MALWARE CnC = bad host","severity":1}}`
	var ev alertEvent
	if err := json.Unmarshal([]byte(line), &ev); err != nil {
		t.Fatalf("разбор: %v", err)
	}
	cef := ev.toCEF()
	if !strings.HasPrefix(cef, "CEF:0|SuriFleet|Suricata|1.0|2051001|") {
		t.Errorf("префикс CEF = %q", cef)
	}
	// '=' в подписи экранирован (\=), severity 1 → 8.
	if !strings.Contains(cef, `bad host|8|`) {
		t.Errorf("подпись/severity = %q", cef)
	}
	if !strings.Contains(cef, "src=192.0.2.5") || !strings.Contains(cef, "dst=198.51.100.9") || !strings.Contains(cef, "proto=TCP") {
		t.Errorf("extension = %q", cef)
	}
	// CEF-экранирование '=' в extension.
	esc := cefEscape("a=b\\c")
	if esc != `a\=b\\c` {
		t.Errorf("cefEscape = %q", esc)
	}
	// severity-маппинг.
	if sevToCEF(1) != 8 || sevToCEF(2) != 5 || sevToCEF(3) != 2 {
		t.Error("sevToCEF")
	}
}

func TestNewAlertsFiltersAndRotation(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "eve.json")
	content := alertJSON(1, "sig one", "192.0.2.1") + "\n" +
		`{"timestamp":"t","event_type":"stats","stats":{}}` + "\n" + // не alert — пропускается
		alertJSON(2, "sig two", "192.0.2.2") + "\n"
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f := newSIEMForwarder(p, "127.0.0.1:9", "udp", "cef")
	alerts := f.newAlerts()
	if len(alerts) != 2 {
		t.Fatalf("alert-событий = %d, ожидается 2 (stats пропущен)", len(alerts))
	}
	if alerts[0].Alert.SignatureID != 1 || alerts[1].Alert.SignatureID != 2 {
		t.Errorf("sid = %d/%d", alerts[0].Alert.SignatureID, alerts[1].Alert.SignatureID)
	}
	// Повторный вызов — новых нет (offset продвинут).
	if got := f.newAlerts(); len(got) != 0 {
		t.Errorf("повтор = %d, ожидается 0", len(got))
	}
	// Дозапись — только новое событие.
	f2, _ := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	f2.WriteString(alertJSON(3, "sig three", "192.0.2.3") + "\n")
	f2.Close()
	if got := f.newAlerts(); len(got) != 1 || got[0].Alert.SignatureID != 3 {
		t.Errorf("инкремент = %v", got)
	}
	// Ротация: файл усечён → читаем с нуля.
	os.WriteFile(p, []byte(alertJSON(4, "rotated", "192.0.2.4")+"\n"), 0o644)
	if got := f.newAlerts(); len(got) != 1 || got[0].Alert.SignatureID != 4 {
		t.Errorf("после ротации = %v", got)
	}
}

func TestForwardOnceUDP(t *testing.T) {
	// UDP-сервер-заглушка: ловим CEF-датаграммы.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("udp listen: %v", err)
	}
	defer pc.Close()
	addr := pc.LocalAddr().String()

	dir := t.TempDir()
	p := filepath.Join(dir, "eve.json")
	os.WriteFile(p, []byte(alertJSON(7, "test sig", "192.0.2.7")+"\n"), 0o644)

	f := newSIEMForwarder(p, addr, "udp", "cef")
	sent, err := f.forwardOnce()
	if err != nil {
		t.Fatalf("forwardOnce: %v", err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, ожидается 1", sent)
	}
	pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 2048)
	n, _, err := pc.ReadFrom(buf)
	if err != nil {
		t.Fatalf("udp read: %v", err)
	}
	got := string(buf[:n])
	if !strings.HasPrefix(got, "CEF:0|SuriFleet|") || !strings.Contains(got, "test sig") {
		t.Errorf("датаграмма = %q", got)
	}
}
