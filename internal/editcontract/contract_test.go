package editcontract

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type reviewerFunc func(context.Context, Review) (Decision, error)

func (f reviewerFunc) Review(ctx context.Context, r Review) (Decision, error) { return f(ctx, r) }
func testLimits() Limits {
	return Limits{InputBytes: 4096, FinalBytes: 4096, Lines: 100, PathBytes: 128, PreviewBytes: 16384}
}
func fixture(t *testing.T) (*Workspace, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	w, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return w, dir
}
func prepared(t *testing.T, w *Workspace) *Proposal {
	t.Helper()
	p, err := w.Prepare(context.Background(), "file.txt", []byte("proposed\n"), testLimits())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

var allow = reviewerFunc(func(context.Context, Review) (Decision, error) { return Allow, nil })

func TestBindingCopiesAndReadOnly(t *testing.T) {
	w, dir := fixture(t)
	input := []byte("proposed\r\n\x1b\u202e\\\"\n")
	p, err := w.Prepare(context.Background(), "file.txt", input, testLimits())
	if err != nil {
		t.Fatal(err)
	}
	want := string(input)
	input[0] = 'X'
	r := p.View()
	if r.Path != "file.txt" || r.Original.SHA256 != digest([]byte("original\r\n")) || r.ProposedSHA256 != digest([]byte(want)) || r.Limits != testLimits() {
		t.Fatal(r)
	}
	if !strings.Contains(r.Display, strconv.QuoteToASCII(want)) || !strings.Contains(r.Display, r.ID) {
		t.Fatal("incomplete review")
	}
	for _, c := range r.Display {
		if (c < 32 && c != '\n') || c > 126 {
			t.Fatal("unsafe display")
		}
	}
	r.Path = "other"
	r.Limits.InputBytes = 1
	r.Display = "tampered"
	permit, err := p.Approve(context.Background(), reviewerFunc(func(_ context.Context, got Review) (Decision, error) {
		if got.Path != "file.txt" || got.Display == "tampered" {
			t.Fatal("mutable view")
		}
		got.Path = "changed"
		return Allow, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	copyProposal := *p
	if _, err := copyProposal.Approve(context.Background(), allow); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
	copyPermit := *permit
	v, err := permit.Consume(context.Background())
	if err != nil || string(v.Proposed) != want {
		t.Fatal(v, err)
	}
	v.Proposed[0] = 'X'
	if _, err := copyPermit.Consume(context.Background()); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "file.txt"))
	if err != nil || string(data) != "original\r\n" {
		t.Fatal("contract wrote file", err)
	}
}

func TestInvalidAndLimitsPreventReview(t *testing.T) {
	w, _ := fixture(t)
	for _, p := range []string{"", ".", "../file.txt", "/file.txt", "a/../file.txt", "a\\file.txt", "C:file.txt", "NUL", "name.", "a//b"} {
		if got, err := w.Prepare(context.Background(), p, []byte("x"), testLimits()); got != nil || !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("%q: %v", p, err)
		}
	}
	for _, limits := range []Limits{{}, {InputBytes: -1, Lines: 1, PathBytes: 1, PreviewBytes: 1}, {InputBytes: 1024*1024 + 1, Lines: 1, PathBytes: 1, PreviewBytes: 1}} {
		if _, err := w.Prepare(context.Background(), "file.txt", nil, limits); !errors.Is(err, ErrInvalid) {
			t.Fatal(err)
		}
	}
	for _, change := range []func(*Limits){func(l *Limits) { l.InputBytes = 1 }, func(l *Limits) { l.PathBytes = 1 }, func(l *Limits) { l.PreviewBytes = 1 }, func(l *Limits) { l.Lines = 1 }} {
		limits := testLimits()
		change(&limits)
		if p, err := w.Prepare(context.Background(), "file.txt", []byte("x\ny\n"), limits); p != nil || !errors.Is(err, ErrLimit) {
			t.Fatal(p, err)
		}
	}
	if _, err := w.Prepare(context.Background(), "file.txt", []byte{0xff}, testLimits()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := (&Proposal{}).Approve(context.Background(), allow); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if _, err := (&Permit{}).Consume(context.Background()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
}

func TestCompletePreviewExactLimit(t *testing.T) {
	w, _ := fixture(t)
	p := prepared(t, w)
	limits := testLimits()
	limits.PreviewBytes = len(p.View().Display)
	// Header length can change with the limit's number of digits; stabilize it.
	for i := 0; i < 5; i++ {
		q, err := w.Prepare(context.Background(), "file.txt", []byte("proposed\n"), limits)
		if err != nil {
			limits.PreviewBytes++
			continue
		}
		if len(q.View().Display) == limits.PreviewBytes {
			p = q
			break
		}
		limits.PreviewBytes = len(q.View().Display)
	}
	q, err := w.Prepare(context.Background(), "file.txt", []byte("proposed\n"), limits)
	if err != nil || len(q.View().Display) != limits.PreviewBytes {
		t.Fatalf("exact: %v", err)
	}
	limits.PreviewBytes--
	if p, err := w.Prepare(context.Background(), "file.txt", []byte("proposed\n"), limits); p != nil || !errors.Is(err, ErrLimit) {
		t.Fatal(p, err)
	}
}

func TestVersionChangesInvalidate(t *testing.T) {
	for _, stage := range []string{"before", "during", "after", "replacement", "removed"} {
		t.Run(stage, func(t *testing.T) {
			w, dir := fixture(t)
			p := prepared(t, w)
			target := filepath.Join(dir, "file.txt")
			calls := 0
			change := func() {
				if err := os.WriteFile(target, []byte("changed\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			reviewer := reviewerFunc(func(context.Context, Review) (Decision, error) {
				calls++
				if stage == "during" {
					change()
				}
				return Allow, nil
			})
			if stage == "before" {
				change()
			}
			permit, err := p.Approve(context.Background(), reviewer)
			if stage == "before" || stage == "during" {
				if permit != nil || !errors.Is(err, ErrChanged) {
					t.Fatal(permit, err)
				}
				if stage == "before" && calls != 0 {
					t.Fatal("stale proposal displayed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if stage == "after" {
				change()
			}
			if stage == "replacement" {
				other := filepath.Join(dir, "other")
				if err := os.WriteFile(other, []byte("original\r\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(other, target); err != nil {
					t.Fatal(err)
				}
			}
			if stage == "removed" {
				if err := os.Remove(target); err != nil {
					t.Fatal(err)
				}
			}
			v, err := permit.Consume(context.Background())
			if err == nil || v.Proposed != nil {
				t.Fatal("changed target validated")
			}
			if _, err := permit.Consume(context.Background()); !errors.Is(err, ErrUsed) {
				t.Fatal(err)
			}
		})
	}
}

func TestCancellationAndReviewerFailures(t *testing.T) {
	w, _ := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.Prepare(ctx, "file.txt", nil, testLimits()); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, decision := range []Decision{Deny, Decision(99)} {
		p := prepared(t, w)
		if token, err := p.Approve(context.Background(), reviewerFunc(func(context.Context, Review) (Decision, error) { return decision, nil })); token != nil || !errors.Is(err, ErrDenied) {
			t.Fatal(err)
		}
		if _, err := p.Approve(context.Background(), allow); !errors.Is(err, ErrUsed) {
			t.Fatal(err)
		}
	}
	p := prepared(t, w)
	if _, err := p.Approve(context.Background(), reviewerFunc(func(context.Context, Review) (Decision, error) { return Allow, errors.New("sensitive") })); !errors.Is(err, ErrApproval) || strings.Contains(err.Error(), "sensitive") {
		t.Fatal(err)
	}
	p = prepared(t, w)
	during, stop := context.WithCancel(context.Background())
	if token, err := p.Approve(during, reviewerFunc(func(context.Context, Review) (Decision, error) { stop(); return Allow, nil })); token != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	p = prepared(t, w)
	permit, err := p.Approve(context.Background(), allow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := permit.Consume(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := permit.Consume(context.Background()); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
}

func TestSymlinksAndNonRegularTargets(t *testing.T) {
	w, dir := fixture(t)
	external := t.TempDir()
	if err := os.WriteFile(filepath.Join(external, "target"), []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, link := range []struct{ name, target string }{{"inside", "file.txt"}, {"outside", filepath.Join(external, "target")}, {"directory", external}} {
		if err := os.Symlink(link.target, filepath.Join(dir, link.name)); err != nil {
			t.Fatalf("symlink permission required: %v", err)
		}
		p := link.name
		if link.name == "directory" {
			p += "/target"
		}
		if proposal, err := w.Prepare(context.Background(), p, nil, testLimits()); proposal != nil || !errors.Is(err, ErrSymlink) {
			t.Fatal(proposal, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "folder"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"folder", "missing"} {
		if _, err := w.Prepare(context.Background(), p, nil, testLimits()); !errors.Is(err, ErrFile) {
			t.Fatal(err)
		}
	}
	p := prepared(t, w)
	permit, err := p.Approve(context.Background(), allow)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "file.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(external, "target"), filepath.Join(dir, "file.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := permit.Consume(context.Background()); !errors.Is(err, ErrSymlink) {
		t.Fatal(err)
	}
}

type shortWriter struct{}

func (shortWriter) Write(b []byte) (int, error) { return len(b) - 1, nil }

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("sensitive writer error") }
func TestTerminalFullDisplayAndDenial(t *testing.T) {
	w, _ := fixture(t)
	for _, answer := range []string{"y\n", "\n", "n\n", "always\n", "y", ""} {
		p := prepared(t, w)
		var out bytes.Buffer
		permit, err := p.Approve(context.Background(), NewTerminal(strings.NewReader(answer), &out))
		if answer == "y\n" {
			if err != nil || permit == nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, ErrDenied) || permit != nil {
			t.Fatal(answer, err)
		}
		if !strings.HasPrefix(out.String(), p.View().Display) {
			t.Fatal("incomplete display")
		}
	}
	for _, writer := range []io.Writer{shortWriter{}, errorWriter{}} {
		p := prepared(t, w)
		if token, err := p.Approve(context.Background(), NewTerminal(strings.NewReader("y\n"), writer)); token != nil || !errors.Is(err, ErrApproval) {
			t.Fatal(err)
		}
	}
}

func TestTerminalCancellation(t *testing.T) {
	w, _ := fixture(t)
	p := prepared(t, w)
	input, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel when the complete display is written; no answer is accepted.
	terminal := NewTerminal(input, writerFunc(func(b []byte) (int, error) { cancel(); return len(b), nil }))
	if token, err := p.Approve(ctx, terminal); token != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestCancellationWhileWaitingForAnswer(t *testing.T) {
	w, _ := fixture(t)
	p := prepared(t, w)
	input, output := io.Pipe()
	defer input.Close()
	defer output.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ready := make(chan struct{})
	terminal := NewTerminal(input, writerFunc(func(b []byte) (int, error) {
		if strings.Contains(string(b), "[y/N]") {
			close(ready)
		}
		return len(b), nil
	}))
	done := make(chan error, 1)
	go func() { _, err := p.Approve(ctx, terminal); done <- err }()
	<-ready
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := p.Approve(context.Background(), allow); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
}

func TestInvalidOriginalAndClosedWorkspace(t *testing.T) {
	w, dir := fixture(t)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte{0xff}, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Prepare(context.Background(), "file.txt", nil, testLimits()); !errors.Is(err, ErrInvalid) {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p := prepared(t, w)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if permit, err := p.Approve(context.Background(), allow); permit != nil || !errors.Is(err, ErrFile) {
		t.Fatal(permit, err)
	}
}

func TestTerminalOversizedAnswerCannotLeakIntoNextReview(t *testing.T) {
	w, _ := fixture(t)
	var out bytes.Buffer
	terminal := NewTerminal(strings.NewReader(strings.Repeat("x", 8192)+"\ny\n"), &out)
	if permit, err := prepared(t, w).Approve(context.Background(), terminal); permit != nil || !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
	if permit, err := prepared(t, w).Approve(context.Background(), terminal); permit != nil || !errors.Is(err, ErrApproval) {
		t.Fatal(err)
	}
}

func TestMetadataOnlyChangeAndDeadline(t *testing.T) {
	w, dir := fixture(t)
	p := prepared(t, w)
	permit, err := p.Approve(context.Background(), allow)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "file.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(dir, "file.txt"), info.ModTime().Add(-1000000000), info.ModTime().Add(-1000000000)); err != nil {
		t.Fatal(err)
	}
	if _, err := permit.Consume(context.Background()); !errors.Is(err, ErrChanged) {
		t.Fatal(err)
	}
	p = prepared(t, w)
	ctx, cancel := context.WithDeadline(context.Background(), info.ModTime().Add(-1000000000))
	defer cancel()
	if _, err := p.Approve(ctx, allow); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

type writerFunc func([]byte) (int, error)

func TestApprovalContextCannotBeReplaced(t *testing.T) {
	w, _ := fixture(t)
	p := prepared(t, w)
	ctx, cancel := context.WithCancel(context.Background())
	permit, err := p.Approve(ctx, allow)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if v, err := permit.Consume(context.Background()); v.Proposed != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(v, err)
	}
	if _, err := permit.Consume(context.Background()); !errors.Is(err, ErrUsed) {
		t.Fatal(err)
	}
}

func (f writerFunc) Write(b []byte) (int, error) { return f(b) }
