package main

import (
	"testing"

	agentv1 "github.com/surifleet/surifleet/internal/gen/agent/v1"
)

func TestServiceActionVerb(t *testing.T) {
	cases := []struct {
		action agentv1.ServiceAction
		verb   string
		ok     bool
	}{
		{agentv1.ServiceAction_SERVICE_ACTION_RELOAD, "reload", true},
		{agentv1.ServiceAction_SERVICE_ACTION_RESTART, "restart", true},
		{agentv1.ServiceAction_SERVICE_ACTION_STOP, "stop", true},
		{agentv1.ServiceAction_SERVICE_ACTION_START, "start", true},
		{agentv1.ServiceAction_SERVICE_ACTION_UNSPECIFIED, "", false},
		{agentv1.ServiceAction(99), "", false},
	}
	for _, c := range cases {
		verb, ok := serviceActionVerb(c.action)
		if verb != c.verb || ok != c.ok {
			t.Errorf("serviceActionVerb(%v) = (%q,%v), хотим (%q,%v)", c.action, verb, ok, c.verb, c.ok)
		}
	}
}

func TestFirstLine(t *testing.T) {
	if got := firstLine("active\n"); got != "active" {
		t.Errorf("firstLine с переводом строки = %q", got)
	}
	if got := firstLine("inactive"); got != "inactive" {
		t.Errorf("firstLine без перевода строки = %q", got)
	}
	if got := firstLine(""); got != "" {
		t.Errorf("firstLine пустой = %q", got)
	}
}
