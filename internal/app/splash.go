package app

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/chzyer/readline"
)

// splashArt is the Fluxa mark as terminal cells: the rounded badge as a
// traced outline, the wordmark's F, and the wave rising left to right
// beneath it. Every row must be exactly splashWidth columns wide —
// TestSplashArtIsRectangular enforces that, because the redraw below
// moves the cursor up by a fixed number of lines and a row that wrapped
// would shift every following frame.
var splashArt = []string{
	"╭────────────────────────────╮",
	"│                            │",
	"│    ██████████████          │",
	"│    ██████████████          │",
	"│    █████                   │",
	"│    █████                   │",
	"│    ███████████             │",
	"│    ███████████             │",
	"│    █████             ~~~~~~│",
	"│    █████      ~~~~~~~      │",
	"│   ~~~~~~~~~~~~             │",
	"│                            │",
	"╰────────────────────────────╯",
}

// splashWidth is the column count of every splashArt row.
const splashWidth = 30

const (
	// splashFrames × splashFrameInterval is the drawing time: a little
	// under two seconds, long enough to read as a hand tracing the mark
	// and short enough that nobody waits on it to start working.
	splashFrames        = 45
	splashFrameInterval = 40 * time.Millisecond

	ansiHideCursor = "\033[?25l"
	ansiShowCursor = "\033[?25h"
)

// splashClass is what a cell of splashArt belongs to, which decides both
// its color and when it gets drawn.
type splashClass int

const (
	splashBlank splashClass = iota
	splashFrame
	splashMark
	splashWave
)

func splashClassOf(cell rune) splashClass {
	switch cell {
	case ' ':
		return splashBlank
	case '█':
		return splashMark
	case '~':
		return splashWave
	default:
		return splashFrame
	}
}

// splashRunes converts splashArt into an indexable grid.
func splashRunes() [][]rune {
	grid := make([][]rune, len(splashArt))
	for index, row := range splashArt {
		grid[index] = []rune(row)
	}
	return grid
}

// splashDrawOrder lists every non-blank cell in the order a hand would
// draw the mark: the badge outline traced clockwise from its top-left
// corner, then the F stroke by stroke from the top, then the wave
// sweeping left to right. Revealing cells in this order is what makes the
// animation read as drawing rather than as a fade-in.
func splashDrawOrder(grid [][]rune) [][2]int {
	height := len(grid)
	if height == 0 {
		return nil
	}
	width := len(grid[0])

	var frame, mark, wave [][2]int
	appendIf := func(target *[][2]int, want splashClass, row, col int) {
		if splashClassOf(grid[row][col]) == want {
			*target = append(*target, [2]int{row, col})
		}
	}

	// Clockwise: top edge, right edge, bottom edge, left edge.
	for col := 0; col < width; col++ {
		appendIf(&frame, splashFrame, 0, col)
	}
	for row := 1; row < height; row++ {
		appendIf(&frame, splashFrame, row, width-1)
	}
	for col := width - 2; col >= 0; col-- {
		appendIf(&frame, splashFrame, height-1, col)
	}
	for row := height - 2; row >= 1; row-- {
		appendIf(&frame, splashFrame, row, 0)
	}

	// The F top to bottom, as it would be written.
	for row := 0; row < height; row++ {
		for col := 0; col < width; col++ {
			appendIf(&mark, splashMark, row, col)
		}
	}
	// The wave left to right, as a single sweeping stroke.
	for col := 0; col < width; col++ {
		for row := 0; row < height; row++ {
			appendIf(&wave, splashWave, row, col)
		}
	}

	order := make([][2]int, 0, len(frame)+len(mark)+len(wave))
	order = append(order, frame...)
	order = append(order, mark...)
	return append(order, wave...)
}

// splashColor is the escape sequence a class is drawn in. The three
// classes deliberately sit at three brightness levels of the one brand
// hue — outline, wave, mark — which is what gives the flat cells the
// layered look the real logo has.
func splashColor(class splashClass) string {
	switch class {
	case splashFrame:
		return ansiDim + ansiTeal
	case splashWave:
		return ansiTeal
	case splashMark:
		return ansiBold + ansiTealBright
	default:
		return ""
	}
}

// renderSplashFrame draws grid with only the first revealed cells of
// order shown, everything else left blank. Each row is always emitted at
// full width so a partial frame never leaves stale cells behind.
func renderSplashFrame(grid [][]rune, order [][2]int, revealed int) string {
	if revealed > len(order) {
		revealed = len(order)
	}
	shown := make([][]bool, len(grid))
	for index := range grid {
		shown[index] = make([]bool, len(grid[index]))
	}
	for _, cell := range order[:revealed] {
		shown[cell[0]][cell[1]] = true
	}

	var out strings.Builder
	for row := range grid {
		current := splashBlank
		for col, cell := range grid[row] {
			class := splashBlank
			if shown[row][col] {
				class = splashClassOf(cell)
			}
			if class != current {
				out.WriteString(ansiReset)
				out.WriteString(splashColor(class))
				current = class
			}
			if class == splashBlank {
				out.WriteRune(' ')
			} else {
				out.WriteRune(cell)
			}
		}
		out.WriteString(ansiReset)
		out.WriteByte('\n')
	}
	return out.String()
}

// splashCaption centers title and subtitle under the drawn mark.
func splashCaption(s style, title, subtitle string) string {
	center := func(plain, styled string) string {
		pad := (splashWidth - visibleWidth(plain)) / 2
		if pad < 0 {
			pad = 0
		}
		return strings.Repeat(" ", pad) + styled
	}
	return center(title, s.bold(title)) + "\n" + center(subtitle, s.dim(subtitle)) + "\n"
}

// drawSplash animates the Fluxa mark over about two seconds and reports
// whether it drew anything. It draws only into a real terminal that is
// actually big enough for the art: the redraw works by moving the cursor
// back up a fixed number of lines, which a wrapped or scrolled frame
// would tear apart, and a piped or NO_COLOR run must stay byte-for-byte
// what it was before this existed (styling disabled is exactly that
// signal). Callers fall back to style.banner when this returns false.
func drawSplash(w io.Writer, s style) bool {
	if !s.enabled {
		return false
	}
	file, ok := w.(*os.File)
	if !ok {
		return false
	}
	width, height, err := readline.GetSize(int(file.Fd())) //nolint:gosec // a real fd is always small; never overflows int.
	// A caption line and the wizard's own first section still have to fit
	// under the art without scrolling it off the redraw region.
	if err != nil || width < splashWidth || height < len(splashArt)+4 {
		return false
	}

	grid := splashRunes()
	order := splashDrawOrder(grid)

	if _, err := fmt.Fprint(w, ansiHideCursor); err != nil {
		return false
	}
	defer func() { _, _ = fmt.Fprint(w, ansiShowCursor) }()

	for frame := 1; frame <= splashFrames; frame++ {
		if frame > 1 {
			if _, err := fmt.Fprintf(w, "\033[%dA", len(grid)); err != nil {
				return true
			}
		}
		revealed := len(order) * frame / splashFrames
		if _, err := fmt.Fprint(w, renderSplashFrame(grid, order, revealed)); err != nil {
			return true
		}
		if frame < splashFrames {
			time.Sleep(splashFrameInterval)
		}
	}
	return true
}
