package ui

// v1.6.38 test: the "Server information" card copy. v1.6.37 shipped a
// reworded sentence ("Version, uptime, etc. is reported by the
// server."); the operator asked to revert the rewording and keep the
// original sentence with exactly one word changed: "as" -> "is". This
// pins all three sides: the new sentence is there, the v1.6.37
// rewording is gone, and the pre-v1.6.37 "as reported" is gone too.

import (
	"strings"
	"testing"
)

func TestServerInfoCardWordingV1638(t *testing.T) {
	got := serverInfoCardSubtitle()
	want := "Version, uptime and configuration is reported by the server."
	if got != want {
		t.Errorf("server info subtitle = %q, want %q", got, want)
	}
	if !strings.Contains(got, "and configuration is reported by the server") {
		t.Errorf("subtitle %q lost the original \"and configuration\" phrasing", got)
	}
	if strings.Contains(got, "etc.") {
		t.Errorf("subtitle %q still carries the v1.6.37 rewording (\"etc.\")", got)
	}
	if strings.Contains(got, "as reported") {
		t.Errorf("subtitle %q regressed to the pre-v1.6.37 \"as reported\" wording", got)
	}
}
