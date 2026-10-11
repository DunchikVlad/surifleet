// update_config_test.go — маппинг строки file → enum (чанк 113).
package httpapi

import "testing"

func TestParseUpdateConfigFile(t *testing.T) {
	ok := map[string]bool{
		"update_yaml":  true,
		"enable_conf":  true,
		"disable_conf": true,
		"modify_conf":  true,
		"source_yaml":  true,
	}
	for in, want := range ok {
		_, got := parseUpdateConfigFile(in)
		if got != want {
			t.Fatalf("%q: got %v", in, got)
		}
	}
	for _, bad := range []string{"", "update.yaml", "sources", "SOURCE_YAML", "enable.conf"} {
		if _, got := parseUpdateConfigFile(bad); got {
			t.Fatalf("%q: мусор не должен проходить", bad)
		}
	}
}
