package app

import (
	"bytes"
	"strings"
	"testing"
)

func TestSpinnerDisabledPrintsStaticLabelOnce(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, style{})
	sp.start("Working")
	sp.finish(true)
	if got := buf.String(); got != "Working...\n" {
		t.Errorf("buf = %q, want a single static \"Working...\\n\" line", got)
	}
}

func TestSpinnerFinishWithoutStartIsSafeNoOp(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, style{enabled: true})
	sp.finish(true)
	sp.finish(false)
	if got := buf.String(); got != "" {
		t.Errorf("buf = %q, want no output from finishing an unstarted spinner", got)
	}
}

func TestSpinnerEnabledStartThenFinishClearsLine(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, style{enabled: true})
	sp.start("Building")
	sp.finish(true)
	if sp.cancel != nil || sp.done != nil {
		t.Fatal("finish() left the spinner's goroutine state non-nil, want it fully stopped")
	}
	if got := buf.String(); !strings.HasSuffix(got, "\r\033[K") {
		t.Errorf("buf = %q, want it to end with the cursor-clearing escape finish() writes", got)
	}
}

func TestSpinnerStartTwiceStopsThePreviousOne(t *testing.T) {
	var buf bytes.Buffer
	sp := newSpinner(&buf, style{enabled: true})
	sp.start("First")
	sp.start("Second")
	sp.finish(true)
	if sp.cancel != nil || sp.done != nil {
		t.Fatal("finish() left the spinner's goroutine state non-nil after a double start()")
	}
}
