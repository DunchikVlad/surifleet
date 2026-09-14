package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Реальный вывод `suricata --build-info` (Ubuntu 26.04, Suricata 8.0.3).
const sampleBuildInfo = `This is Suricata version 8.0.3 RELEASE
Features: NFQ PCAP_SET_BUFF AF_PACKET HAVE_PACKET_FANOUT LIBCAP_NG LIBNET1.1 HAVE_HTP_URI_NORMALIZE_HOOK PCRE_JIT HAVE_NSS HTTP2_DECOMPRESSION HAVE_LUA HAVE_JA3 HAVE_JA4 HAVE_LIBJANSSON TLS TLS_C11 MAGIC RUST POPCNT64
SIMD support: SSE_4_2 SSE_4_1 SSE_3 SSE_2
Atomic intrinsics: 1 2 4 8 16 byte(s)
`

func TestBinVersion(t *testing.T) {
	if v := binVersion(sampleBuildInfo); v != "8.0.3" {
		t.Errorf("binVersion = %q, ожидалось 8.0.3", v)
	}
	if v := binVersion("Suricata version 7.0.10"); v != "7.0.10" {
		t.Errorf("binVersion короткая форма = %q, ожидалось 7.0.10", v)
	}
	if v := binVersion("no version here"); v != "" {
		t.Errorf("binVersion без версии = %q, ожидалось пусто", v)
	}
}

func TestBinFeatures(t *testing.T) {
	got := binFeatures(sampleBuildInfo)
	want := "NFQ PCAP_SET_BUFF AF_PACKET HAVE_PACKET_FANOUT LIBCAP_NG LIBNET1.1 HAVE_HTP_URI_NORMALIZE_HOOK PCRE_JIT HAVE_NSS HTTP2_DECOMPRESSION HAVE_LUA HAVE_JA3 HAVE_JA4 HAVE_LIBJANSSON TLS TLS_C11 MAGIC RUST POPCNT64"
	if got != want {
		t.Errorf("binFeatures = %q", got)
	}
	if got := binFeatures("no features"); got != "" {
		t.Errorf("binFeatures без строки Features = %q, ожидалось пусто", got)
	}
}

// Усечённый вариант /etc/suricata/suricata.yaml (Debian-пакет, af-packet).
const sampleYAML = `%YAML 1.1
---
# Конфигурация Suricata
vars:
  address-groups:
    HOME_NET: "[192.168.0.0/16]"

default-rule-path: /var/lib/suricata/rules
rule-files:
  - suricata.rules

default-log-dir: /var/log/suricata # каталог логов

af-packet:
  - interface: enp0s3
    cluster-id: 99
    cluster-type: cluster_flow
  - interface: default

pcap:
  - interface: eth1
`

func TestParseSuricataYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "suricata.yaml")
	if err := os.WriteFile(path, []byte(sampleYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	rulesDir, logDir, ifaces := parseSuricataYAML(path)
	if rulesDir != "/var/lib/suricata/rules" {
		t.Errorf("rulesDir = %q", rulesDir)
	}
	if logDir != "/var/log/suricata" {
		t.Errorf("logDir = %q (инлайн-комментарий не отрезан?)", logDir)
	}
	if !reflect.DeepEqual(ifaces, []string{"enp0s3"}) {
		t.Errorf("ifaces = %v, ожидалось [enp0s3] (af-packet приоритетнее pcap, default отфильтрован)", ifaces)
	}
}

func TestParseSuricataYAMLPCAPFallback(t *testing.T) {
	yaml := "default-log-dir: /var/log/suricata\n\npcap:\n  - interface: eth1\n  - interface: eth2\n"
	path := filepath.Join(t.TempDir(), "suricata.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	rulesDir, _, ifaces := parseSuricataYAML(path)
	if rulesDir != defaultRulesDir {
		t.Errorf("rulesDir = %q, ожидался дефолт %q", rulesDir, defaultRulesDir)
	}
	if !reflect.DeepEqual(ifaces, []string{"eth1", "eth2"}) {
		t.Errorf("ifaces = %v, ожидалось [eth1 eth2] из pcap", ifaces)
	}
}

func TestScalarValue(t *testing.T) {
	cases := []struct {
		line, key, want string
		ok              bool
	}{
		{"default-log-dir: /var/log/suricata", "default-log-dir", "/var/log/suricata", true},
		{"default-log-dir: /var/log/suricata # comment", "default-log-dir", "/var/log/suricata", true},
		{`default-rule-path: "/opt/rules"`, "default-rule-path", "/opt/rules", true},
		{"interface: enp0s3", "interface", "enp0s3", true},
		{"interface:", "interface", "", false},
		{"other-key: x", "interface", "", false},
	}
	for _, c := range cases {
		got, ok := scalarValue(c.line, c.key)
		if got != c.want || ok != c.ok {
			t.Errorf("scalarValue(%q, %q) = (%q, %v), ожидалось (%q, %v)", c.line, c.key, got, ok, c.want, c.ok)
		}
	}
}

func TestInstanceName(t *testing.T) {
	if got := instanceName("suricata.service"); got != "suricata" {
		t.Errorf("instanceName = %q", got)
	}
	if got := instanceName(""); got != "suricata" {
		t.Errorf("instanceName(\"\") = %q, ожидался дефолт", got)
	}
}

func TestNormalizeUnitState(t *testing.T) {
	for in, want := range map[string]string{
		"active": "active", "failed": "failed", "inactive": "inactive",
		"reloading": "reloading", "activating": "inactive", "": "inactive",
	} {
		if got := normalizeUnitState(in); got != want {
			t.Errorf("normalizeUnitState(%q) = %q, ожидалось %q", in, got, want)
		}
	}
}
