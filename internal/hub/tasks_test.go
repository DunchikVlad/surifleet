package hub

import (
	"testing"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

func TestConfigDeployStatus(t *testing.T) {
	cases := []struct {
		name             string
		succeeded        bool
		validateOnly     bool
		validationPassed bool
		want             string
	}{
		{"applied", true, false, true, "applied"},
		{"validated", true, true, true, "validated"},
		{"validation_failed", false, false, false, "validation_failed"},
		{"validation_failed_validate_only", false, true, false, "validation_failed"},
		{"deploy_failed", false, false, true, "deploy_failed"},
		{"deploy_failed_no_validation", false, false, false, "validation_failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := configDeployStatus(c.succeeded, c.validateOnly, c.validationPassed); got != c.want {
				t.Fatalf("configDeployStatus(%v,%v,%v) = %q, want %q",
					c.succeeded, c.validateOnly, c.validationPassed, got, c.want)
			}
		})
	}
}

func TestUpdateConfigFileName(t *testing.T) {
	cases := []struct {
		file   agentv1.UpdateConfigFile
		source string
		want   string
	}{
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_UPDATE_YAML, "", "update_yaml"},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_ENABLE_CONF, "", "enable_conf"},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML, "et/open", "source_yaml:et/open"},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_SOURCE_YAML, "", "source_yaml"},
		{agentv1.UpdateConfigFile_UPDATE_CONFIG_FILE_UNSPECIFIED, "", "unknown"},
	}
	for _, c := range cases {
		if got := updateConfigFileName(c.file, c.source); got != c.want {
			t.Fatalf("updateConfigFileName(%v, %q) = %q, want %q", c.file, c.source, got, c.want)
		}
	}
}
