package main

import (
	"io"
	"log/slog"
	"sync/atomic"
	"testing"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

func TestSiemSettingsDefaults(t *testing.T) {
	s := siemSettings{Addr: "siem.local:514"}
	if s.protocol() != "udp" {
		t.Errorf("protocol() = %q, ожидалось udp", s.protocol())
	}
	if s.format() != "cef" {
		t.Errorf("format() = %q, ожидалось cef", s.format())
	}
	s2 := siemSettings{Addr: "siem.local:514", Protocol: "tcp", Format: "json"}
	if s2.protocol() != "tcp" || s2.format() != "json" {
		t.Errorf("явные значения перекрыты дефолтами: %+v", s2)
	}
}

func TestSiemFromProto(t *testing.T) {
	sc := &agentv1.SiemConfig{Addr: "10.0.0.5:5514", Protocol: "tcp", Format: "json"}
	got := siemFromProto(sc)
	if got.Addr != "10.0.0.5:5514" || got.Protocol != "tcp" || got.Format != "json" {
		t.Errorf("siemFromProto = %+v", got)
	}
}

// TestHandleServerMessageConfigPushSiem — ConfigPush с AgentConfig.siem
// обновляет shared-конфигурацию (чанк 89); без siem — не трогает.
func TestHandleServerMessageConfigPushSiem(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	levelVar := new(slog.LevelVar)

	var siemCfg atomic.Value
	siemCfg.Store(siemSettings{Addr: "old:514"})

	// ConfigPush без siem — значение не меняется.
	handleServerMessage(&agentv1.ServerMessage{Payload: &agentv1.ServerMessage_ConfigPush{
		ConfigPush: &agentv1.ConfigPush{Config: &agentv1.AgentConfig{LogLevel: "debug"}},
	}}, nil, levelVar, &siemCfg, log)
	if got, _ := siemCfg.Load().(siemSettings); got.Addr != "old:514" {
		t.Fatalf("ConfigPush без siem изменил конфиг: %+v", got)
	}

	// ConfigPush с siem — применяется.
	handleServerMessage(&agentv1.ServerMessage{Payload: &agentv1.ServerMessage_ConfigPush{
		ConfigPush: &agentv1.ConfigPush{Config: &agentv1.AgentConfig{
			Siem: &agentv1.SiemConfig{Addr: "new:6514", Protocol: "tcp", Format: "json"},
		}},
	}}, nil, levelVar, &siemCfg, log)
	got, _ := siemCfg.Load().(siemSettings)
	if got.Addr != "new:6514" || got.Protocol != "tcp" || got.Format != "json" {
		t.Fatalf("ConfigPush siem не применён: %+v", got)
	}

	// Явное выключение (пустой addr) тоже применяется.
	handleServerMessage(&agentv1.ServerMessage{Payload: &agentv1.ServerMessage_ConfigPush{
		ConfigPush: &agentv1.ConfigPush{Config: &agentv1.AgentConfig{
			Siem: &agentv1.SiemConfig{Addr: ""},
		}},
	}}, nil, levelVar, &siemCfg, log)
	if got, _ := siemCfg.Load().(siemSettings); got.Addr != "" {
		t.Fatalf("выключение SIEM не применено: %+v", got)
	}
}
