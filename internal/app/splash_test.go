package app

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

// ansiPattern strips styling so a rendered frame can be compared against
// the art it was drawn from.
var ansiPattern = regexp.MustCompile("\x1b\\[[0-9;?]*[a-zA-Z]")

// TestSplashArtIsRectangular guards the invariant the redraw depends on:
// every frame is reprinted after moving the cursor up by exactly
// len(splashArt) lines, so a row wider than the rest would wrap on a
// narrow-but-allowed terminal and shift every following frame down.
func TestSplashArtIsRectangular(t *testing.T) {
	t.Parallel()

	for index, row := range splashArt {
		if got := len([]rune(row)); got != splashWidth {
			t.Errorf("splashArt[%d] is %d columns wide, want %d", index, got, splashWidth)
		}
	}
}

// TestSplashDrawOrderCoversEveryCellOnce keeps the animation honest: a
// cell missing from the order is never drawn at all, and a duplicated one
// silently steals a slot from the reveal budget.
func TestSplashDrawOrderCoversEveryCellOnce(t *testing.T) {
	t.Parallel()

	grid := splashRunes()
	order := splashDrawOrder(grid)

	seen := make(map[[2]int]bool, len(order))
	for _, cell := range order {
		if seen[cell] {
			t.Fatalf("cell %v appears twice in the draw order", cell)
		}
		seen[cell] = true
	}

	want := 0
	for _, row := range grid {
		for _, cell := range row {
			if splashClassOf(cell) != splashBlank {
				want++
			}
		}
	}
	if len(order) != want {
		t.Errorf("draw order covers %d cells, want every one of the %d non-blank cells", len(order), want)
	}
}

// TestSplashDrawOrderRunsFrameThenMarkThenWave is what makes the
// animation read as a hand tracing the logo rather than as noise
// appearing: the badge outline first, then the F, then the wave.
func TestSplashDrawOrderRunsFrameThenMarkThenWave(t *testing.T) {
	t.Parallel()

	grid := splashRunes()
	order := splashDrawOrder(grid)

	previous := splashFrame
	for index, cell := range order {
		class := splashClassOf(grid[cell[0]][cell[1]])
		if class < previous {
			t.Fatalf("cell %d of the draw order is class %d after class %d — groups must not interleave", index, class, previous)
		}
		previous = class
	}
	if previous != splashWave {
		t.Errorf("draw order ends on class %d, want the wave drawn last", previous)
	}
}

// TestRenderSplashFrameFullyRevealedMatchesArt proves the animation
// converges on exactly the intended picture, and that partial frames pad
// to full width so no earlier frame's cells survive underneath.
func TestRenderSplashFrameFullyRevealedMatchesArt(t *testing.T) {
	t.Parallel()

	grid := splashRunes()
	order := splashDrawOrder(grid)

	rendered := ansiPattern.ReplaceAllString(renderSplashFrame(grid, order, len(order)), "")
	want := strings.Join(splashArt, "\n") + "\n"
	if rendered != want {
		t.Errorf("fully revealed frame =\n%s\nwant\n%s", rendered, want)
	}

	half := ansiPattern.ReplaceAllString(renderSplashFrame(grid, order, len(order)/2), "")
	for index, row := range strings.Split(strings.TrimSuffix(half, "\n"), "\n") {
		if got := len([]rune(row)); got != splashWidth {
			t.Errorf("half-drawn row %d is %d columns wide, want %d", index, got, splashWidth)
		}
	}
}

// TestRenderSplashFrameRevealsNothingAtZero covers the first frame: an
// empty canvas of the right shape, not a blank string that would leave
// the following cursor-up moving over the wrong lines.
func TestRenderSplashFrameRevealsNothingAtZero(t *testing.T) {
	t.Parallel()

	grid := splashRunes()
	rendered := ansiPattern.ReplaceAllString(renderSplashFrame(grid, splashDrawOrder(grid), 0), "")
	rows := strings.Split(strings.TrimSuffix(rendered, "\n"), "\n")
	if len(rows) != len(splashArt) {
		t.Fatalf("empty frame has %d rows, want %d", len(rows), len(splashArt))
	}
	if strings.TrimSpace(rendered) != "" {
		t.Errorf("empty frame = %q, want only spaces", rendered)
	}
}

// TestDrawSplashSkipsNonTerminalWriters is the guarantee that keeps every
// scripted, piped, and NO_COLOR run byte-identical to what it was before
// the animation existed — including a two-second delay nobody asked a
// pipeline to wait through.
func TestDrawSplashSkipsNonTerminalWriters(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if drawSplash(&buf, style{enabled: true}) {
		t.Error("drawSplash(bytes.Buffer) = true, want false — only a real terminal can be redrawn")
	}
	if buf.Len() != 0 {
		t.Errorf("drawSplash wrote %q to a non-terminal, want nothing", buf.String())
	}
	if drawSplash(&buf, style{}) {
		t.Error("drawSplash with styling disabled = true, want false")
	}
}

func TestSplashCaptionCentersUnstyled(t *testing.T) {
	t.Parallel()

	caption := splashCaption(style{}, "Fluxa Builder", "interactive project setup")
	for _, line := range strings.Split(strings.TrimSuffix(caption, "\n"), "\n") {
		if len([]rune(line)) > splashWidth {
			t.Errorf("caption line %q is wider than the mark it sits under", line)
		}
		if strings.TrimSpace(line) == "" {
			t.Errorf("caption line %q is empty", line)
		}
	}
}
