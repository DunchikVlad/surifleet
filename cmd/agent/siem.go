// siem.go — пересылка EVE-алертов Suricata в SIEM (чанк 45, п. 5.4 ТЗ:
// «пересылка EVE-алертов … в SIEM»). Агент читает eve.json (тот же
// offset-tail, что и у метрик — устойчив к ротации) и пересылает события
// event_type=alert в SIEM по syslog: UDP (RFC 5426) или TCP (RFC 6587
// octet-counting), в формате CEF (Common Event Format) или сыром JSON.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// siemMaxRead — предел чтения eve.json за тик пересылки (16 МБ, как метрики).
const siemMaxRead = 16 << 20

// alertEvent — alert-событие eve.json (нужная часть для SIEM).
type alertEvent struct {
	Timestamp string `json:"timestamp"`
	EventType string `json:"event_type"`
	SrcIP     string `json:"src_ip"`
	SrcPort   int    `json:"src_port"`
	DestIP    string `json:"dest_ip"`
	DestPort  int    `json:"dest_port"`
	Proto     string `json:"proto"`
	Alert     struct {
		SignatureID int    `json:"signature_id"`
		Signature   string `json:"signature"`
		Severity    int    `json:"severity"`
	} `json:"alert"`
	Raw json.RawMessage `json:"-"`
}

// cefEscape экранирует значение для CEF extension (RFC: \ = \\, = — \=,
// перевод строки — пробел).
func cefEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `=`, `\=`, "\n", " ", "\r", " ")
	return r.Replace(s)
}

// cefHeaderEscape — экранирование заголовочных полей CEF (| и \).
func cefHeaderEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `|`, `\|`, "\n", " ", "\r", " ")
	return r.Replace(s)
}

// sevToCEF — Suricata severity (1 высший..3 низший) → CEF severity (0..10).
func sevToCEF(sev int) int {
	switch sev {
	case 1:
		return 8
	case 2:
		return 5
	default:
		return 2
	}
}

// toCEF — alert-событие → CEF-строка:
// CEF:0|Vendor|Product|Version|SignatureID|Name|Severity|Extension
func (e *alertEvent) toCEF() string {
	sigID := strconv.Itoa(e.Alert.SignatureID)
	sev := strconv.Itoa(sevToCEF(e.Alert.Severity))
	ext := "src=" + cefEscape(e.SrcIP) +
		" spt=" + strconv.Itoa(e.SrcPort) +
		" dst=" + cefEscape(e.DestIP) +
		" dpt=" + strconv.Itoa(e.DestPort) +
		" proto=" + cefEscape(e.Proto) +
		" rt=" + cefEscape(e.Timestamp) +
		" cs1Label=SuricataSignatureID cs1=" + sigID
	return "CEF:0|SuriFleet|Suricata|1.0|" + sigID + "|" +
		cefHeaderEscape(e.Alert.Signature) + "|" + sev + "|" + ext
}

// siemConn — syslog-приёмник: UDP (fire-and-forget) или TCP (octet-counting,
// реконнект при обрыве). Не потокобезопасен — один писатель (горутина).
type siemConn struct {
	addr     string
	tcp      bool
	conn     net.Conn
	lastErr  error
	errCount int
}

// send отправляет одно сообщение. UDP — одна датаграмма; TCP — одно
// octet-counted сообщение (RFC 6587: "<len> <payload>").
func (s *siemConn) send(payload []byte) error {
	if !s.tcp {
		c, err := s.dial()
		if err != nil {
			return err
		}
		_, err = c.Write(payload)
		return err
	}
	// TCP: octet-counting. При обрыве — один реконнект.
	for attempt := 0; attempt < 2; attempt++ {
		c, err := s.dial()
		if err != nil {
			return err
		}
		msg := append([]byte(strconv.Itoa(len(payload))+" "), payload...)
		if _, err := c.Write(msg); err != nil {
			s.close() // обрыв — переоткрыть на следующей попытке
			continue
		}
		return nil
	}
	return fmt.Errorf("siem tcp: запись не удалась после реконнекта")
}

// dial — активное соединение (создаёт лениво).
func (s *siemConn) dial() (net.Conn, error) {
	if s.conn != nil {
		return s.conn, nil
	}
	network := "udp"
	if s.tcp {
		network = "tcp"
	}
	c, err := net.DialTimeout(network, s.addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	s.conn = c
	return c, nil
}

// close закрывает соединение (TCP — переоткроется при следующей отправке).
func (s *siemConn) close() {
	if s.conn != nil {
		_ = s.conn.Close()
		s.conn = nil
	}
}

// siemForwarder — пересылка alert-событий eve.json в SIEM.
type siemForwarder struct {
	path   string // eve.json
	format string // cef|json
	conn   *siemConn
	offset int64
}

// newSIEMForwarder — пересыльщик для eve.json по конфигу агента.
// addr пустой — не вызывайте (пересылка выключена).
func newSIEMForwarder(evePath, addr, protocol, format string) *siemForwarder {
	return &siemForwarder{
		path:   evePath,
		format: format,
		conn:   &siemConn{addr: addr, tcp: protocol == "tcp"},
	}
}

// newAlerts читает новые байты eve.json и возвращает alert-события из них
// (устойчив к ротации; усечение чтения — 16 МБ за тик, хвост обрезается).
func (f *siemForwarder) newAlerts() []alertEvent {
	st, err := os.Stat(f.path)
	if err != nil {
		return nil
	}
	if st.Size() < f.offset {
		f.offset = 0 // ротация/усечение
	}
	if st.Size() == f.offset {
		return nil
	}
	file, err := os.Open(f.path)
	if err != nil {
		return nil
	}
	defer file.Close()
	if _, err := file.Seek(f.offset, io.SeekStart); err != nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(file, siemMaxRead))
	if err != nil {
		return nil
	}
	f.offset += int64(len(data))

	var out []alertEvent
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 256<<10), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if !bytes.Contains(line, []byte(`"event_type":"alert"`)) {
			continue
		}
		var ev alertEvent
		if json.Unmarshal(line, &ev) == nil && ev.EventType == "alert" {
			ev.Raw = append(json.RawMessage(nil), line...)
			out = append(out, ev)
		}
	}
	return out
}

// forwardOnce — один проход: прочитать новые алерты и переслать. Возвращает
// число пересланных и последнюю ошибку отправки (nil — всё ушло).
func (f *siemForwarder) forwardOnce() (int, error) {
	alerts := f.newAlerts()
	sent := 0
	var lastErr error
	for i := range alerts {
		var payload []byte
		if f.format == "json" {
			payload = append(alerts[i].Raw, '\n')
		} else {
			payload = []byte(alerts[i].toCEF() + "\n")
		}
		if err := f.conn.send(payload); err != nil {
			lastErr = err
			break // SIEM недоступен — не молотить по каждому событию
		}
		sent++
	}
	return sent, lastErr
}
