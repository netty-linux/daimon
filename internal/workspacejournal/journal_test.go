package workspacejournal

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
)

func fixture() []Metadata {
	return []Metadata{{Type: "create_file", Path: "docs/new.txt", After: strings.Repeat("a", 64)}, {Type: "replace_file", Path: "src/config.txt", Before: strings.Repeat("b", 64), After: strings.Repeat("c", 64)}}
}

func TestOutcomesAndPrivacy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		statuses []string
		want     Summary
	}{
		{"success", []string{"started", "succeeded", "started", "succeeded"}, Summary{Succeeded: 2}},
		{"denied", []string{"denied", "denied"}, Summary{Denied: 2}},
		{"error_before_effect", []string{"failed", "failed"}, Summary{Failed: 2}},
		{"partial", []string{"started", "succeeded", "started", "failed"}, Summary{Succeeded: 1, Unknown: 1, Partial: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			ops := fixture()
			j, err := New(&out, ops)
			if err != nil {
				t.Fatal(err)
			}
			ops[0].Path = "changed"
			index := 0
			for _, status := range tc.statuses {
				path := fixture()[index].Path
				if err := j.Append(path, status); err != nil {
					t.Fatal(err)
				}
				if status != "started" {
					index++
				}
			}
			if got := j.Summary(); got != tc.want {
				t.Fatalf("%+v != %+v", got, tc.want)
			}
			for i, line := range bytes.Split(bytes.TrimSpace(out.Bytes()), []byte{'\n'}) {
				var record Record
				if err := json.Unmarshal(line, &record); err != nil {
					t.Fatal(err)
				}
				if record.Sequence != i+1 || record.Version != 1 || record.Timestamp.IsZero() {
					t.Fatal("invalid metadata")
				}
			}
			if strings.Contains(out.String(), "content") || strings.Contains(out.String(), "Authorization") {
				t.Fatal("unexpected data")
			}
			if j.Append("docs/new.txt", "started") == nil {
				t.Fatal("reused operation")
			}
		})
	}
}

type brokenSink struct {
	short, syncFailure bool
	calls              int
}

func (s *brokenSink) Write(b []byte) (int, error) {
	s.calls++
	if s.short {
		return len(b) - 1, nil
	}
	return len(b), nil
}
func (s *brokenSink) Sync() error {
	if s.syncFailure {
		return errors.New("SECRET must never escape")
	}
	return nil
}
func TestSinkFailureNoRetry(t *testing.T) {
	for _, sink := range []*brokenSink{{short: true}, {syncFailure: true}} {
		j, _ := New(sink, fixture())
		err := j.Append("docs/new.txt", "started")
		if !errors.Is(err, ErrJournal) || strings.Contains(err.Error(), "SECRET") {
			t.Fatal("unsafe error")
		}
		if j.Append("src/config.txt", "started") == nil || sink.calls != 1 || !j.Summary().SinkFailed {
			t.Fatal("journal retried")
		}
	}
}
func TestConcurrentAndInvalid(t *testing.T) {
	var out bytes.Buffer
	j, _ := New(&out, fixture())
	var wg sync.WaitGroup
	for _, op := range fixture() {
		wg.Add(1)
		go func(path string) {
			defer wg.Done()
			if err := j.Append(path, "started"); err != nil {
				t.Error(err)
			}
			if err := j.Append(path, "succeeded"); err != nil {
				t.Error(err)
			}
		}(op.Path)
	}
	wg.Wait()
	if j.Summary().Succeeded != 2 {
		t.Fatal("lost records")
	}
	for _, path := range []string{"../escape", "/outside", "a\\b", "a\x00b"} {
		ops := fixture()
		ops[0].Path = path
		if _, err := New(io.Discard, ops); err == nil {
			t.Fatal("unsafe path accepted")
		}
	}
	if _, err := New(nil, fixture()); err == nil {
		t.Fatal("nil sink")
	}
	if j.Append("docs/new.txt", "SECRET") == nil {
		t.Fatal("free status accepted")
	}
}
