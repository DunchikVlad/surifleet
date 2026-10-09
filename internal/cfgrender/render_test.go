package cfgrender

import (
	"errors"
	"strings"
	"testing"
)

var testTarget = Target{
	InstanceID:   "i-1",
	InstanceName: "sensor-1",
	ConfigPath:   "/etc/suricata/suricata.yaml",
	RulesDir:     "/etc/suricata/rules",
	LogDir:       "/var/log/suricata",
	Interface:    "enp0s3",
	HostID:       "h-1",
	Hostname:     "test1",
	HostIP:       "192.168.31.67",
	ClusterID:    "c-1",
	ClusterName:  "edge",
}

func mustRender(t *testing.T, chain []Profile, target Target) (string, []Source) {
	t.Helper()
	yml, src, err := Render(chain, target)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	return yml, src
}

func TestRenderSingleProfilePassthrough(t *testing.T) {
	yml, src := mustRender(t, []Profile{{ID: "p1", Name: "base", ScopeType: "cluster", Content: "vars: {iface: enp0s3}\naf-packet:\n  interface: \"{{iface}}\"\n"}}, testTarget)
	if !strings.Contains(yml, "interface: enp0s3") {
		t.Fatalf("нет подстановки vars:\n%s", yml)
	}
	if strings.Contains(yml, "vars:") {
		t.Fatalf("vars попал в вывод:\n%s", yml)
	}
	if len(src) != 1 || src[0].ProfileID != "p1" || src[0].ScopeType != "cluster" {
		t.Fatalf("sources: %+v", src)
	}
}

func TestRenderChainDeepMerge(t *testing.T) {
	cluster := Profile{ID: "c", Name: "cluster", ScopeType: "cluster", Content: `
vars: {threads: 4, iface: enp0s3}
outputs:
  eve-log:
    enabled: true
    types: [alert]
af-packet:
  interface: "{{iface}}"
  threads: "{{threads}}"
  cluster-id: 99
`}
	host := Profile{ID: "h", Name: "host", ScopeType: "host", Content: `
vars: {threads: 8}
outputs:
  eve-log:
    enabled: false
af-packet:
  cluster-id: null
`}
	yml, src := mustRender(t, []Profile{cluster, host}, testTarget)

	// vars: хост перекрыл threads, iface достался от кластера
	if !strings.Contains(yml, "threads: 8") {
		t.Fatalf("vars host не перекрыли:\n%s", yml)
	}
	if !strings.Contains(yml, "interface: enp0s3") {
		t.Fatalf("vars cluster потерялись:\n%s", yml)
	}
	// deep merge map outputs: enabled перекрыт, types унаследованы
	if !strings.Contains(yml, "enabled: false") {
		t.Fatalf("enabled не перекрыт:\n%s", yml)
	}
	if !strings.Contains(yml, "- alert") {
		t.Fatalf("types не унаследованы:\n%s", yml)
	}
	// null удалил ключ cluster-id, соседние ключи af-packet остались
	if strings.Contains(yml, "cluster-id") {
		t.Fatalf("null не удалил ключ:\n%s", yml)
	}
	if len(src) != 2 || src[0].ProfileID != "c" || src[1].ProfileID != "h" {
		t.Fatalf("sources не root→tip: %+v", src)
	}
}

func TestRenderBuiltinTargetVars(t *testing.T) {
	p := Profile{ID: "p", Name: "t", ScopeType: "instance", Content: `
vars: {suffix: X}
default-rule-path: "{{instance.rules_dir}}"
home-net: "[{{host.ip}}]"
sensor: "{{ cluster.name }}/{{host.hostname}}/{{instance.name}}{{suffix}}"
`}
	yml, _ := mustRender(t, []Profile{p}, testTarget)
	for _, want := range []string{
		"default-rule-path: /etc/suricata/rules",
		"home-net: '[192.168.31.67]'",
		"sensor: edge/test1/sensor-1X",
	} {
		if !strings.Contains(yml, want) {
			t.Fatalf("нет %q в:\n%s", want, yml)
		}
	}
}

func TestRenderTypedSubstitution(t *testing.T) {
	p := Profile{ID: "p", Name: "t", ScopeType: "instance", Content: `
vars: {threads: 8, evelog: true, note: "8 cores"}
af-packet:
  threads: "{{threads}}"
  enable: "{{evelog}}"
  label: "{{threads}} threads on {{instance.interface}}"
`}
	yml, _ := mustRender(t, []Profile{p}, testTarget)
	if !strings.Contains(yml, "threads: 8\n") {
		t.Fatalf("int не подставлен числом:\n%s", yml)
	}
	if !strings.Contains(yml, "enable: true") {
		t.Fatalf("bool не подставлен:\n%s", yml)
	}
	if !strings.Contains(yml, `label: 8 threads on enp0s3`) {
		t.Fatalf("встроенная подстановка:\n%s", yml)
	}
}

func TestRenderUnknownVarsStrict(t *testing.T) {
	p := Profile{ID: "p", Name: "t", ScopeType: "instance", Content: "a: \"{{nope}}\"\nb: \"{{instance.missing}}\"\n"}
	_, _, err := Render([]Profile{p}, testTarget)
	if err == nil {
		t.Fatal("ожидалась ошибка неизвестных переменных")
	}
	var uv *UnknownVarsError
	if !errors.As(err, &uv) {
		t.Fatalf("не UnknownVarsError: %T %v", err, err)
	}
	if len(uv.Names) != 2 || uv.Names[0] != "instance.missing" || uv.Names[1] != "nope" {
		t.Fatalf("список имён: %+v", uv.Names)
	}
}

func TestRenderErrors(t *testing.T) {
	cases := []struct {
		name    string
		chain   []Profile
		wantErr string
	}{
		{"пустая цепочка", nil, "пустая цепочка"},
		{"некорректный YAML", []Profile{{ID: "p", Name: "bad", Content: "a: [unclosed"}}, "некорректный YAML"},
		{"vars не map", []Profile{{ID: "p", Name: "bad", Content: "vars: [1,2]"}}, "vars"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := Render(tc.chain, testTarget)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ошибка = %v, ожидалось содержащее %q", err, tc.wantErr)
			}
		})
	}
}

func TestRenderLiteralBracesKept(t *testing.T) {
	p := Profile{ID: "p", Name: "t", ScopeType: "instance", Content: "note: \"no closing {{ brace\"\nok: \"{{cluster.name}}\"\n"}
	yml, _ := mustRender(t, []Profile{p}, testTarget)
	if !strings.Contains(yml, "{{ brace") {
		t.Fatalf("буквальные скобки потерялись:\n%s", yml)
	}
	if !strings.Contains(yml, "ok: edge") {
		t.Fatalf("подстановка рядом с буквальными скобками сломана:\n%s", yml)
	}
}

func TestRenderVarsNestedMerge(t *testing.T) {
	cluster := Profile{ID: "c", Name: "c", ScopeType: "cluster", Content: `
vars:
  eve:
    enabled: true
    types: [alert]
`}
	host := Profile{ID: "h", Name: "h", ScopeType: "host", Content: `
vars:
  eve:
    types: [alert, http]
eve: "{{eve}}"
`}
	yml, _ := mustRender(t, []Profile{cluster, host}, testTarget)
	// глубокий мерж vars: enabled от кластера, types перекрыты хостом
	if !strings.Contains(yml, "enabled: true") || !strings.Contains(yml, "- http") {
		t.Fatalf("вложенный мерж vars не сработал:\n%s", yml)
	}
}

func TestRenderPreservesYamlHeader(t *testing.T) {
	p := Profile{ID: "p", Name: "t", ScopeType: "instance", Content: "%YAML 1.1\n---\nvars: {iface: enp0s3}\naf-packet:\n  interface: \"{{iface}}\"\n"}
	yml, _, err := Render([]Profile{p}, testTarget)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(yml, "%YAML 1.1\n---\n") {
		t.Fatalf("заголовок потерян:\n%.60s", yml)
	}
	// и без заголовка во входе — рендер добавляет (совместимость suricata)
	p2 := Profile{ID: "p", Name: "t", ScopeType: "instance", Content: "vars: {iface: enp0s3}\naf-packet:\n  interface: \"{{iface}}\"\n"}
	yml2, _, err := Render([]Profile{p2}, testTarget)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.HasPrefix(yml2, "%YAML 1.1\n---\n") {
		t.Fatalf("заголовок не добавлен:\n%.60s", yml2)
	}
}
