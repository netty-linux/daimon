package diffview

import (
	"errors"
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

// Reconstruct both inputs from the edit script, checking every equal row.
// Repeated lines and asymmetric edits exercise ambiguous Myers paths.
func TestEditScriptReconstructsInputs(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 2000; trial++ {
		makeLines := func() []textLine {
			lines := make([]textLine, rng.Intn(20))
			for i := range lines {
				lines[i] = textLine{text: strconv.Itoa(rng.Intn(4)), terminated: true}
			}
			return lines
		}
		a, b := makeLines(), makeLines()
		x, y := 0, 0
		for _, op := range diffOps(a, b) {
			switch op {
			case opEqual:
				if x >= len(a) || y >= len(b) || !equalLine(a[x], b[y]) {
					t.Fatalf("trial %d: invalid equal at %d,%d", trial, x, y)
				}
				x++
				y++
			case opDelete:
				if x >= len(a) {
					t.Fatalf("trial %d: invalid deletion", trial)
				}
				x++
			case opInsert:
				if y >= len(b) {
					t.Fatalf("trial %d: invalid insertion", trial)
				}
				y++
			default:
				t.Fatalf("unknown operation %d", op)
			}
		}
		if x != len(a) || y != len(b) {
			t.Fatalf("trial %d: incomplete script %d,%d", trial, x, y)
		}
	}
}

func TestExactOutputLimit(t *testing.T) {
	want := render(t, "p", "a\n", "b\n")
	for _, delta := range []int{0, -1} {
		out, err := Render("p", "a\n", "b\n", Limits{MaxLines: 1, MaxBytes: len(want) + delta})
		if delta == 0 && (err != nil || out != want) {
			t.Fatalf("exact limit: %q %v", out, err)
		}
		if delta == -1 && (!errors.Is(err, ErrTooLarge) || out != "") {
			t.Fatalf("exceeded limit: %q %v", out, err)
		}
	}
}

func TestHardInputCapsAndInvalidPath(t *testing.T) {
	limits := Limits{MaxLines: int(^uint(0) >> 1), MaxBytes: int(^uint(0) >> 1)}
	for _, input := range []string{strings.Repeat("x\n", maxInputLines+1), strings.Repeat("x", maxInputBytes+1)} {
		if out, err := Render("p", input, input, limits); out != "" || !errors.Is(err, ErrTooLarge) {
			t.Fatalf("input cap: %q %v", out, err)
		}
	}
	if _, err := Render("\xff", "a", "b", limits); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("path: %v", err)
	}
}

func generousLimits() Limits { return Limits{MaxLines: 1000, MaxBytes: 16 * 1024} }

func render(t *testing.T, path, oldText, newText string) string {
	t.Helper()
	out, err := Render(path, oldText, newText, generousLimits())
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEqualFilesRenderHeadersOnly(t *testing.T) {
	out := render(t, "a.txt", "same\nlines\n", "same\nlines\n")
	if out != "--- a.txt\n+++ a.txt\n" {
		t.Fatalf("output=%q", out)
	}
	if strings.Contains(out, "@@") {
		t.Fatal("equal files must not produce hunks")
	}
}

func TestSingleLineChange(t *testing.T) {
	out := render(t, "p", "a\nb\nc\n", "a\nB\nc\n")
	want := "--- p\n+++ p\n@@ -1,3 +1,3 @@\n a\n-b\n+B\n c\n"
	if out != want {
		t.Fatalf("output=%q want=%q", out, want)
	}
}

func TestInsertion(t *testing.T) {
	out := render(t, "p", "a\n", "a\nb\n")
	want := "--- p\n+++ p\n@@ -1,1 +1,2 @@\n a\n+b\n"
	if out != want {
		t.Fatalf("output=%q want=%q", out, want)
	}
}

func TestRemoval(t *testing.T) {
	out := render(t, "p", "a\nb\n", "a\n")
	want := "--- p\n+++ p\n@@ -1,2 +1,1 @@\n a\n-b\n"
	if out != want {
		t.Fatalf("output=%q want=%q", out, want)
	}
}

func TestEmptySides(t *testing.T) {
	out := render(t, "p", "", "x\n")
	if want := "--- p\n+++ p\n@@ -0,0 +1,1 @@\n+x\n"; out != want {
		t.Fatalf("output=%q want=%q", out, want)
	}
	out = render(t, "p", "x\n", "")
	if want := "--- p\n+++ p\n@@ -1,1 +0,0 @@\n-x\n"; out != want {
		t.Fatalf("output=%q want=%q", out, want)
	}
}

func TestCRLFEndingsVisible(t *testing.T) {
	out := render(t, "p", "a\r\nb\r\n", "a\r\nB\r\n")
	for _, want := range []string{" a␍", "-b␍", "+B␍"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
	// An ending-only change is still a change.
	out = render(t, "p", "a\r\n", "a\n")
	if !strings.Contains(out, "-a␍") || !strings.Contains(out, "+a\n") {
		t.Fatalf("ending change hidden: %q", out)
	}
}

func TestMissingFinalNewline(t *testing.T) {
	out := render(t, "p", "a\nb", "a\nb\n")
	if !strings.Contains(out, "-b\n"+noNewlineMarker+"\n+b\n") {
		t.Fatalf("marker misplaced: %q", out)
	}
	if got := strings.Count(out, noNewlineMarker); got != 1 {
		t.Fatalf("markers=%d: %q", got, out)
	}
}

func TestControlCharactersSanitized(t *testing.T) {
	out := render(t, "p", "a\x1bb\n", "a\x1bB \u009bc ‮d\n")
	for _, forbidden := range []string{"\x1b", " \u009b", " ‮"} {
		if strings.Contains(out, forbidden) {
			t.Fatalf("control reached output: %q", out)
		}
	}
	for _, want := range []string{"-a?b", "+a?B ?c ?d"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}
}

func TestMultipleRegionsKeepEveryChange(t *testing.T) {
	changed := map[int]bool{5: true, 30: true, 55: true}
	oldLines := make([]string, 0, 60)
	newLines := make([]string, 0, 60)
	for i := 1; i <= 60; i++ {
		old := "line-" + strconv.Itoa(i)
		oldLines = append(oldLines, old)
		if changed[i] {
			newLines = append(newLines, "CHANGED-"+strconv.Itoa(i))
		} else {
			newLines = append(newLines, old)
		}
	}
	out := render(t, "p", strings.Join(oldLines, "\n")+"\n", strings.Join(newLines, "\n")+"\n")
	if got := strings.Count(out, "@@ -"); got != 3 {
		t.Fatalf("hunks=%d, want 3: %q", got, out)
	}
	for _, i := range []int{5, 30, 55} {
		if !strings.Contains(out, "-"+oldLines[i-1]+"\n") || !strings.Contains(out, "+"+newLines[i-1]+"\n") {
			t.Fatalf("changed line %d omitted: %q", i, out)
		}
	}
	if got := strings.Count(out, collapseMarker+"\n"); got != 2 {
		t.Fatalf("collapse markers=%d, want 2: %q", got, out)
	}
}

func TestOutputDeterministic(t *testing.T) {
	oldText := "header\nline one\nline two\nline three\nfooter\n"
	newText := "header\nline ONE\ninserted\nline two\nline three\nfooter\ntail\n"
	first := render(t, "p", oldText, newText)
	for i := 0; i < 25; i++ {
		if out := render(t, "p", oldText, newText); out != first {
			t.Fatalf("render %d differs", i)
		}
	}
}

func TestLineLimitFailsClosed(t *testing.T) {
	big := strings.Repeat("x\n", 1001)
	if _, err := Render("p", big, "y\n", generousLimits()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v", err)
	}
	if _, err := Render("p", "y\n", big, generousLimits()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("err=%v", err)
	}
}

func TestByteLimitFailsWithoutTruncation(t *testing.T) {
	limits := Limits{MaxLines: 1000, MaxBytes: 10}
	out, err := Render("p", "a\n", "b\n", limits)
	if !errors.Is(err, ErrTooLarge) || out != "" {
		t.Fatalf("output=%q err=%v", out, err)
	}
}

func TestOversizedPreviewFailsClosed(t *testing.T) {
	oldText := strings.Repeat("old line here\n", 200)
	newText := strings.Repeat("new line here\n", 200)
	out, err := Render("p", oldText, newText, Limits{MaxLines: 1000, MaxBytes: 64})
	if !errors.Is(err, ErrTooLarge) || out != "" {
		t.Fatalf("output=%q err=%v", out, err)
	}
}

func TestInvalidLimitsAndInput(t *testing.T) {
	for _, limits := range []Limits{{}, {MaxLines: 10}, {MaxBytes: 10}, {MaxLines: -1, MaxBytes: 10}} {
		if _, err := Render("p", "a\n", "b\n", limits); !errors.Is(err, ErrInvalidLimits) {
			t.Fatalf("%+v: %v", limits, err)
		}
	}
	if _, err := Render("p", "a\xffb\n", "b\n", generousLimits()); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("err=%v", err)
	}
}

func TestPathStaysOnHeaderLines(t *testing.T) {
	out := render(t, "a\nb", "x\n", "y\n")
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "--- ") && line != "--- a?b" {
			t.Fatalf("header=%q", line)
		}
		if strings.HasPrefix(line, "+++ ") && line != "+++ a?b" {
			t.Fatalf("header=%q", line)
		}
	}
}
