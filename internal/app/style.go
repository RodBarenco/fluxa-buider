package app

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"
)

// style renders wizard output with ANSI colors and Unicode symbols when
// writing to a real terminal, and leaves messages completely unchanged
// otherwise — piped output, NO_COLOR set, or a non-terminal writer such
// as a test's bytes.Buffer. This is deliberate, not just a nicety:
// existing (and future) tests assert on exact wizard output text, and
// this way they never see an ANSI escape or symbol at all, by
// construction, rather than by remembering to strip them.
type style struct {
	enabled bool
}

// newStyle decides once, at wizard startup, whether w is worth styling
// for: a real attached terminal, not redirected to a file or pipe, and
// without NO_COLOR set (https://no-color.org).
func newStyle(w io.Writer) style {
	if os.Getenv("NO_COLOR") != "" {
		return style{}
	}
	file, ok := w.(*os.File)
	if !ok {
		return style{}
	}
	return style{enabled: isTerminalFile(file)}
}

// isTerminalFile reports whether f is a real attached terminal. Shared by
// newStyle (is stdout worth coloring) and the wizard's decision to use a
// real line-editing readline session (is stdin an actual keyboard, not
// piped or test-provided input) instead of its plain bufio.Reader
// fallback. This delegates to readline's own termios-based isatty check
// rather than the weaker os.ModeCharDevice test: /dev/null and other
// non-terminal character devices pass that check too, which would have
// misfired both of the above for anything redirected from them.
func isTerminalFile(f *os.File) bool {
	return readline.IsTerminal(int(f.Fd())) //nolint:gosec // a real fd is always small; never overflows int.
}

const (
	ansiReset = "\033[0m"
	ansiBold  = "\033[1m"
	ansiDim   = "\033[2m"
	ansiGreen = "\033[32m"
	ansiRed   = "\033[31m"
	// ansiYellow marks a caution — something that still works but is not
	// what a release build should ship with, e.g. source-exposed mode.
	ansiYellow = "\033[33m"
	// ansiTeal is the Fluxa brand color (the wordmark's wave/teal), used as
	// this wizard's accent for section headers and the shell-prompt path —
	// everywhere generic emphasis or a directory path is shown, not the
	// semantic ok/bad colors above, which stay green/red on purpose.
	ansiTeal = "\033[38;2;20;158;168m"
	// ansiTealBright is the same brand hue lifted for text that has to read
	// as foreground (titles, the answer caret, menu keys) rather than as
	// the frame around it.
	ansiTealBright = "\033[38;2;56;208;218m"
)

// frameWidth is the inner width of the banner box and the length section
// rules are padded out to. 62 keeps every line comfortably inside the
// 80-column terminal that is still the safe floor.
const frameWidth = 62

// ok prefixes message with a green checkmark. Unstyled, message passes
// through unchanged.
func (s style) ok(message string) string {
	if !s.enabled {
		return message
	}
	return ansiGreen + "✓ " + ansiReset + message
}

// bad prefixes message with a red cross. Unstyled, message passes
// through unchanged. Named to avoid colliding with *wizard.fail, an
// unrelated method that prints an error and returns a process exit code.
func (s style) bad(message string) string {
	if !s.enabled {
		return message
	}
	return ansiRed + "✗ " + ansiReset + message
}

// warn prefixes message with a yellow exclamation, for the middle ground
// between ok and bad: something proceeded, but not in the shape a real
// release wants. Unstyled, message passes through unchanged.
func (s style) warn(message string) string {
	if !s.enabled {
		return message
	}
	return ansiYellow + "! " + ansiReset + message
}

// bold renders message bold in the Fluxa brand teal, for section headers
// and the wizard's own title. Unstyled, message passes through unchanged.
func (s style) bold(message string) string {
	if !s.enabled {
		return message
	}
	return ansiTeal + ansiBold + message + ansiReset
}

// dim renders message in the terminal's faint attribute, for the
// explanatory text under a question — present when you look for it,
// never competing with the question itself. Unstyled, message passes
// through unchanged.
func (s style) dim(message string) string {
	if !s.enabled {
		return message
	}
	return ansiDim + message + ansiReset
}

// accent renders message in the brand teal without bolding it, for
// inline values worth spotting inside a sentence (a path, a target
// name). Unstyled, message passes through unchanged.
func (s style) accent(message string) string {
	if !s.enabled {
		return message
	}
	return ansiTealBright + message + ansiReset
}

// banner is the wizard's opening title card. Unstyled it degrades to the
// plain title and subtitle on their own lines, so redirected output and
// tests never see box-drawing characters.
func (s style) banner(title, subtitle string) string {
	if !s.enabled {
		return title + "\n" + subtitle + "\n"
	}
	rule := strings.Repeat("─", frameWidth)
	titleLine := "  ⬢  " + title
	subtitleLine := "     " + subtitle
	var out strings.Builder
	out.WriteString(ansiTeal + "╭" + rule + "╮" + ansiReset + "\n")
	out.WriteString(ansiTeal + "│" + ansiReset + ansiBold + ansiTealBright + padVisible(titleLine, frameWidth) + ansiReset + ansiTeal + "│" + ansiReset + "\n")
	out.WriteString(ansiTeal + "│" + ansiReset + ansiDim + padVisible(subtitleLine, frameWidth) + ansiReset + ansiTeal + "│" + ansiReset + "\n")
	out.WriteString(ansiTeal + "╰" + rule + "╯" + ansiReset + "\n")
	return out.String()
}

// step renders a numbered section header, so a long interactive run
// always shows where it is and how much is left. Unstyled it degrades to
// a plain "[2/5] Target platform" line carrying the same information.
func (s style) step(index, total int, title string) string {
	label := fmt.Sprintf("[%d/%d] %s", index, total, title)
	if !s.enabled {
		return "\n" + label + "\n"
	}
	trail := frameWidth - visibleWidth(label) - 3
	if trail < 0 {
		trail = 0
	}
	return "\n" + ansiTealBright + "▌ " + ansiBold + label + ansiReset +
		" " + ansiDim + strings.Repeat("─", trail) + ansiReset + "\n"
}

// field renders one aligned "label  value" summary row. The label column
// is padded identically styled or not, so a summary block lines up in a
// log file exactly as it does on a terminal.
func (s style) field(label, value string) string {
	padded := fmt.Sprintf("  %-9s ", label+":")
	if !s.enabled {
		return padded + value
	}
	return ansiDim + padded + ansiReset + value
}

// menuItem renders one selectable choice: its key, what it produces, and
// a dim note explaining the consequence of picking it.
func (s style) menuItem(key, label, note string) string {
	if !s.enabled {
		return strings.TrimRight(fmt.Sprintf("  %-3s %-14s %s", key, label, note), " ")
	}
	return "  " + ansiTealBright + ansiBold + fmt.Sprintf("%-3s", key) + ansiReset +
		fmt.Sprintf("%-14s", label) + " " + ansiDim + note + ansiReset
}

// question decorates a prompt with the caret that marks "this line wants
// an answer", distinguishing it at a glance from the explanatory output
// above it. Unstyled, the prompt is written exactly as the caller built
// it, which is what every scripted-stdin test already asserts against.
func (s style) question(prompt string) string {
	if !s.enabled {
		return prompt
	}
	return ansiTealBright + "❯ " + ansiReset + prompt
}

// shellPrompt renders userAtHost and path the way a classic bash prompt
// does — green "user@host", the Fluxa brand teal for path, a trailing
// "$ " — so the wizard's project-directory question visually mirrors an
// actual terminal prompt, reinforcing the mental model it now also uses
// for path resolution: relative paths behave as if typed at this exact
// prompt. Unstyled, it degrades to plain "user@host:path$ " text.
func (s style) shellPrompt(userAtHost, path string) string {
	if !s.enabled {
		return userAtHost + ":" + path + "$ "
	}
	return ansiGreen + userAtHost + ansiReset + ":" + ansiTeal + path + ansiReset + "$ "
}

// visibleWidth counts the columns text occupies, which for every string
// this file pads is its rune count — these are wizard-authored labels,
// never arbitrary user text, and none of them carry escape sequences.
func visibleWidth(text string) int {
	return len([]rune(text))
}

// padVisible right-pads text with spaces to exactly width columns,
// leaving anything already at or past that width alone.
func padVisible(text string, width int) string {
	if pad := width - visibleWidth(text); pad > 0 {
		return text + strings.Repeat(" ", pad)
	}
	return text
}
