package app

import (
	"bytes"
	"testing"
)

func TestNewStyleDisabledForNonFileWriter(t *testing.T) {
	var buf bytes.Buffer
	s := newStyle(&buf)
	if s.enabled {
		t.Fatal("newStyle(bytes.Buffer) enabled = true, want false — a non-terminal writer must never be styled")
	}
}

func TestNewStyleRespectsNoColor(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	var buf bytes.Buffer
	s := newStyle(&buf)
	if s.enabled {
		t.Fatal("newStyle() with NO_COLOR set enabled = true, want false")
	}
}

func TestStyleDisabledPassesMessageThroughUnchanged(t *testing.T) {
	var s style // zero value: disabled
	for _, message := range []string{"Project loaded", "Directory not found: /x", "Optional settings:"} {
		if got := s.ok(message); got != message {
			t.Errorf("ok(%q) = %q, want unchanged", message, got)
		}
		if got := s.bad(message); got != message {
			t.Errorf("bad(%q) = %q, want unchanged", message, got)
		}
		if got := s.bold(message); got != message {
			t.Errorf("bold(%q) = %q, want unchanged", message, got)
		}
	}
}

func TestStyleEnabledWrapsMessage(t *testing.T) {
	s := style{enabled: true}
	if got := s.ok("hi"); got == "hi" || got == "" {
		t.Errorf("ok(%q) with styling enabled = %q, want a wrapped/decorated string", "hi", got)
	}
	if got := s.bad("hi"); got == "hi" {
		t.Errorf("bad(%q) with styling enabled = %q, want a wrapped/decorated string", "hi", got)
	}
	if got := s.bold("hi"); got == "hi" {
		t.Errorf("bold(%q) with styling enabled = %q, want a wrapped/decorated string", "hi", got)
	}
}
