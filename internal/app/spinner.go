package app

import (
	"fmt"
	"io"
	"time"
)

// spinnerFrames is a standard braille-dot animation, the same style used
// by most modern CLI tools (npm, cargo, etc.).
var spinnerFrames = []rune("⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏")

const spinnerInterval = 90 * time.Millisecond

// spinner shows an animated "working" indicator in place (redrawn via a
// carriage return) while a long, otherwise-silent operation runs — the
// Docker-based toolchain acquisition and Mesa3D fallback download can
// each take minutes with no output at all otherwise, which reads as a
// hang, not progress. On a non-terminal (style disabled: piped output,
// NO_COLOR, or a test's bytes.Buffer) this degrades to a single static
// "label..." line and no animation, so logged/redirected output never
// fills up with carriage-return noise.
type spinner struct {
	w      io.Writer
	style  style
	cancel chan struct{}
	done   chan struct{}
}

func newSpinner(w io.Writer, s style) *spinner {
	return &spinner{w: w, style: s}
}

// start begins animating label. A still-running previous spinner is
// silently finished first — callers are expected to finish before
// starting the next one, but this stays safe either way.
func (sp *spinner) start(label string) {
	sp.finish(true)
	if !sp.style.enabled {
		_, _ = fmt.Fprintf(sp.w, "%s...\n", label)
		return
	}
	cancel := make(chan struct{})
	done := make(chan struct{})
	sp.cancel, sp.done = cancel, done
	go func() {
		defer close(done)
		ticker := time.NewTicker(spinnerInterval)
		defer ticker.Stop()
		frame := 0
		for {
			select {
			case <-cancel:
				return
			case <-ticker.C:
				_, _ = fmt.Fprintf(sp.w, "\r\033[K%c %s", spinnerFrames[frame%len(spinnerFrames)], label)
				frame++
			}
		}
	}()
}

// finish stops any active animation and clears its line. ok selects
// whether a subsequent message (printed separately by the caller, via
// style.ok/style.bad) reads as a success or failure — finish itself
// prints nothing but the cleared line. Finishing when nothing is active
// is a safe no-op, so callers (including wizardConfirmer, ahead of every
// interactive question) can call it unconditionally.
func (sp *spinner) finish(ok bool) {
	_ = ok // reserved: a future variant may render an inline ✓/✗ itself.
	if sp.cancel == nil {
		return
	}
	close(sp.cancel)
	<-sp.done
	sp.cancel, sp.done = nil, nil
	if sp.style.enabled {
		_, _ = fmt.Fprint(sp.w, "\r\033[K")
	}
}
