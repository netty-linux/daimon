// Package diffview renders deterministic unified-like previews for human
// approval of exact file replacements. Output is never truncated: when the
// configured limits prevent a complete preview, Render fails closed.
package diffview

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// ErrTooLarge reports a preview that does not fit the configured limits.
	ErrTooLarge = errors.New("diff exceeds preview limits")
	// ErrInvalidLimits reports non-positive preview limits.
	ErrInvalidLimits = errors.New("invalid diff limits")
	// ErrInvalidInput reports non-UTF-8 preview input.
	ErrInvalidInput = errors.New("invalid diff input")
)

// Limits bound the preview before any quadratic work and cap the rendered
// output. Inputs additionally have hard caps of 1000 lines per side and
// 1 MiB per string (including path). Zero never means unlimited.
type Limits struct {
	// MaxLines caps each side, counted in lines.
	MaxLines int
	// MaxBytes caps the rendered output, counted in bytes.
	MaxBytes int
}

// contextLines sets how many unchanged lines surround each hunk; longer
// equal runs collapse into a "..." marker. No changed line is ever omitted.
const contextLines = 3

// Hard input caps bound the trace even when a caller supplies huge limits.
const maxInputLines = 1000
const maxInputBytes = 1024 * 1024

// noNewlineMarker flags a final line without a terminator. Content lines
// always carry a ' ', '-' or '+' prefix, so the marker is unambiguous.
const noNewlineMarker = `\ No newline at end of file`

// collapseMarker stands for omitted unchanged lines.
const collapseMarker = "..."

// carriageReturnSuffix marks a CRLF line ending that plain display would
// hide. U+240D is a printable symbol, so it survives control sanitizing.
const carriageReturnSuffix = "␍"

// textLine is one source line with its terminator class. Only a file-final
// line may lack a terminator; equality requires the same text and class so
// ending-only changes always surface as changed lines.
type textLine struct {
	text       string // without terminator
	crlf       bool   // ended with \r\n
	terminated bool   // ended with \n, with or without \r
}

func splitLines(s string) []textLine {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	lines := make([]textLine, 0, len(parts))
	for i, part := range parts {
		last := i == len(parts)-1
		if last && part == "" {
			break // trailing newline leaves no phantom line
		}
		line := textLine{text: part, terminated: !last}
		if !last && strings.HasSuffix(part, "\r") {
			line.text = part[:len(part)-1]
			line.crlf = true
		}
		lines = append(lines, line)
	}
	return lines
}

func equalLine(a, b textLine) bool {
	return a.text == b.text && a.crlf == b.crlf && a.terminated == b.terminated
}

// sanitizeDisplay replaces control and formatting characters with '?' so
// approved text can never shape the terminal. Structure markers are added
// separately and never pass through here.
func sanitizeDisplay(text string) string {
	if strings.IndexFunc(text, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	}) < 0 {
		return text
	}
	var builder strings.Builder
	builder.Grow(len(text))
	for _, r := range text {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			r = '?'
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

// sanitizePath keeps one path on one header line: any control or formatting
// character, including newlines, becomes '?'.
func sanitizePath(path string) string { return sanitizeDisplay(path) }

type editOp uint8

const (
	opEqual editOp = iota
	opDelete
	opInsert
)

// diffOps returns the shortest edit script turning a into b.
// Myers O((N+M)*D) time; the int32 trace costs O((N+M)*D) cells worst case.
// Both are pre-capped by Limits.MaxLines on each side, and the routine uses
// no maps, so output is deterministic.
func diffOps(a, b []textLine) []editOp {
	n, m := len(a), len(b)
	max := n + m
	if max == 0 {
		return nil
	}
	off := max + 1
	v := make([]int32, 2*max+3)
	var trace [][]int32
	done := false
	for d := 0; d <= max && !done; d++ {
		for k := -d; k <= d; k += 2 {
			var x int32
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - int32(k)
			for x < int32(n) && y < int32(m) && equalLine(a[x], b[y]) {
				x++
				y++
			}
			v[off+k] = x
			if x >= int32(n) && y >= int32(m) {
				done = true
				break
			}
		}
		trace = append(trace, append([]int32(nil), v...))
	}
	var ops []editOp
	x, y := int32(n), int32(m)
	for dd := len(trace) - 1; dd > 0; dd-- {
		vp := trace[dd-1]
		k := int(x - y)
		prev := k - 1
		if k == -dd || (k != dd && vp[off+k-1] < vp[off+k+1]) {
			prev = k + 1
		}
		prevX := vp[off+prev]
		prevY := prevX - int32(prev)
		// Walk the equal suffix before undoing the edit that preceded it.
		for x > prevX && y > prevY {
			x--
			y--
			ops = append(ops, opEqual)
		}
		if prev == k+1 {
			y--
			ops = append(ops, opInsert)
		} else {
			x--
			ops = append(ops, opDelete)
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		ops = append(ops, opEqual)
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// opLine is one aligned row: exactly one index is -1 for insertions and
// deletions.
type opLine struct {
	op       editOp
	old, new int
}

func align(ops []editOp) []opLine {
	lines := make([]opLine, 0, len(ops))
	old, new := 0, 0
	for _, op := range ops {
		switch op {
		case opEqual:
			lines = append(lines, opLine{op: op, old: old, new: new})
			old++
			new++
		case opDelete:
			lines = append(lines, opLine{op: op, old: old, new: -1})
			old++
		case opInsert:
			lines = append(lines, opLine{op: op, old: -1, new: new})
			new++
		}
	}
	return lines
}

// groupHunks clusters changes with contextLines of context on each side.
// Equal runs longer than twice the context split groups, so hunks never
// overlap and no changed line is omitted. Each hunk carries the count of
// old/new lines preceding it, which numbers empty sides exactly.
func groupHunks(lines []opLine) []hunk {
	var hunks []hunk
	i := 0
	for i < len(lines) {
		next := i
		for next < len(lines) && lines[next].op == opEqual {
			next++
		}
		if next == len(lines) {
			break
		}
		from := next
		to := next + 1
		for {
			after := to
			for after < len(lines) && lines[after].op == opEqual {
				after++
			}
			if after == len(lines) || after-to > 2*contextLines {
				break
			}
			to = after + 1
		}
		start := from - contextLines
		if start < 0 {
			start = 0
		}
		end := to + contextLines
		if end > len(lines) {
			end = len(lines)
		}
		h := hunk{lines: lines[start:end]}
		for _, line := range lines[:start] {
			if line.old >= 0 {
				h.oldPos++
			}
			if line.new >= 0 {
				h.newPos++
			}
		}
		hunks = append(hunks, h)
		i = to
	}
	return hunks
}

// hunk is one changed region with context and exact preceding counts.
type hunk struct {
	lines          []opLine
	oldPos, newPos int
}

// hunkHeader numbers hunks with 1-based unified offsets. An empty side
// starts at the count of lines preceding the hunk.
func hunkHeader(h hunk) string {
	oldStart, oldCount, newStart, newCount := h.oldPos, 0, h.newPos, 0
	for _, line := range h.lines {
		if line.old >= 0 {
			if oldCount == 0 {
				oldStart = line.old + 1
			}
			oldCount++
		}
		if line.new >= 0 {
			if newCount == 0 {
				newStart = line.new + 1
			}
			newCount++
		}
	}
	return fmt.Sprintf("@@ -%d,%d +%d,%d @@", oldStart, oldCount, newStart, newCount)
}

// Render returns the deterministic preview turning oldText into newText:
// path headers, one hunk per changed region with unified offsets, and one
// ' '/'-'/'+' row per shown line. Lines render sanitized with visible
// endings; over-limit input or output fails closed without truncation.
func Render(path, oldText, newText string, limits Limits) (string, error) {
	if limits.MaxLines <= 0 || limits.MaxBytes <= 0 {
		return "", ErrInvalidLimits
	}
	if !utf8.ValidString(path) || !utf8.ValidString(oldText) || !utf8.ValidString(newText) {
		return "", ErrInvalidInput
	}
	lineLimit := min(limits.MaxLines, maxInputLines)
	lineCount := func(s string) int {
		count := strings.Count(s, "\n")
		if s != "" && !strings.HasSuffix(s, "\n") {
			count++
		}
		return count
	}
	if len(path) > maxInputBytes || len(oldText) > maxInputBytes || len(newText) > maxInputBytes ||
		lineCount(oldText) > lineLimit || lineCount(newText) > lineLimit {
		return "", ErrTooLarge
	}
	oldLines, newLines := splitLines(oldText), splitLines(newText)
	if len(oldLines) > limits.MaxLines || len(newLines) > limits.MaxLines {
		return "", ErrTooLarge
	}
	var out strings.Builder
	write := func(s string) (string, error) {
		if len(s) > limits.MaxBytes-out.Len() {
			return "", ErrTooLarge
		}
		out.WriteString(s)
		return out.String(), nil
	}
	head := "--- " + sanitizePath(path) + "\n+++ " + sanitizePath(path) + "\n"
	if _, err := write(head); err != nil {
		return "", err
	}
	if oldText == newText {
		return out.String(), nil
	}
	for hi, hunk := range groupHunks(align(diffOps(oldLines, newLines))) {
		// Split groups always omit at least one unchanged line between
		// hunks; the bare marker says so without touching content rows.
		if hi > 0 {
			if _, err := write(collapseMarker + "\n"); err != nil {
				return "", err
			}
		}
		if _, err := write(hunkHeader(hunk) + "\n"); err != nil {
			return "", err
		}
		for _, line := range hunk.lines {
			var prefix byte
			var text textLine
			final := false
			switch line.op {
			case opDelete:
				prefix = '-'
				text = oldLines[line.old]
				final = line.old == len(oldLines)-1 && !text.terminated
			case opInsert:
				prefix = '+'
				text = newLines[line.new]
				final = line.new == len(newLines)-1 && !text.terminated
			default:
				prefix = ' '
				text = oldLines[line.old]
				final = (line.old == len(oldLines)-1 && !text.terminated) ||
					(line.new == len(newLines)-1 && !newLines[line.new].terminated)
			}
			row := string(prefix) + sanitizeDisplay(text.text)
			if text.crlf {
				row += carriageReturnSuffix
			}
			if _, err := write(row + "\n"); err != nil {
				return "", err
			}
			if final {
				if _, err := write(noNewlineMarker + "\n"); err != nil {
					return "", err
				}
			}
		}
	}
	return out.String(), nil
}
