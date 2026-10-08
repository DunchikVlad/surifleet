package hub

import "testing"

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
