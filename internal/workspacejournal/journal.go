// Package workspacejournal records bounded metadata to a caller-owned sink.
// It opens no files and cannot apply, replay, retry or roll back operations.
package workspacejournal

import (
	"encoding/json"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var ErrJournal = errors.New("workspace journal unavailable or invalid")

type Metadata struct {
	Type   string `json:"type"`
	Path   string `json:"path"`
	Before string `json:"before,omitempty"`
	After  string `json:"after"`
}
type Record struct {
	Version   int       `json:"version"`
	Sequence  int       `json:"sequence"`
	Timestamp time.Time `json:"timestamp"`
	Operation Metadata  `json:"operation"`
	Status    string    `json:"status"`
}
type Summary struct {
	Succeeded, Denied, Failed, Unknown int
	Partial, SinkFailed                bool
}
type Journal struct {
	mu       sync.Mutex
	sink     io.Writer
	sequence int
	states   map[string]string
	metadata map[string]Metadata
	failed   bool
}

func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// New accepts only metadata, never contents, prompts, errors or credentials.
// A Sync-capable sink is synchronized before Append returns successfully.
func New(sink io.Writer, operations []Metadata) (*Journal, error) {
	if sink == nil || len(operations) == 0 || len(operations) > 2 {
		return nil, ErrJournal
	}
	j := &Journal{sink: sink, states: make(map[string]string), metadata: make(map[string]Metadata)}
	for _, op := range operations {
		if !workspaceplan.ValidPath(op.Path, workspaceplan.DefaultLimits()) || !validHash(op.After) ||
			(op.Type != "create_file" && op.Type != "replace_file") ||
			(op.Type == "create_file" && op.Before != "") ||
			(op.Type == "replace_file" && !validHash(op.Before)) {
			return nil, ErrJournal
		}
		if _, exists := j.states[op.Path]; exists {
			return nil, ErrJournal
		}
		j.states[op.Path] = "prepared"
		j.metadata[op.Path] = op
	}
	return j, nil
}

// Append enforces prepared -> denied/failed/started -> succeeded/failed/unknown.
// "failed" before starting means no operation was begun; after starting it
// means effects are unknown. A sink failure poisons the journal without retry.
// A future executor MUST persist started before any effect and MUST NOT proceed
// if Append fails. This package does not enforce an executor that does not exist.
func (j *Journal) Append(path, status string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	old, exists := j.states[path]
	if j.failed || !exists || j.sequence >= 8 {
		return ErrJournal
	}
	allowed := old == "prepared" && (status == "started" || status == "denied" || status == "failed") ||
		old == "started" && (status == "succeeded" || status == "failed" || status == "unknown")
	if !allowed {
		return ErrJournal
	}
	record := Record{1, j.sequence + 1, time.Now().UTC(), j.metadata[path], status}
	data, err := json.Marshal(record)
	if err != nil {
		return ErrJournal
	}
	data = append(data, '\n')
	n, err := j.sink.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		if sink, ok := j.sink.(interface{ Sync() error }); ok {
			err = sink.Sync()
		}
	}
	if err != nil {
		j.failed = true
		// A completion record may have reached the sink only partially.
		if old == "started" {
			j.states[path] = "unknown"
		}
		return ErrJournal
	}
	j.sequence++
	if old == "started" && status == "failed" {
		status = "unknown"
	}
	j.states[path] = status
	return nil
}

// Summary is public-safe: no paths, hashes, content or identifiers.
// Success records describe caller-reported outcomes, not independently verified IO.
func (j *Journal) Summary() Summary {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := Summary{SinkFailed: j.failed}
	for _, status := range j.states {
		switch status {
		case "succeeded":
			s.Succeeded++
		case "denied":
			s.Denied++
		case "failed":
			s.Failed++
		default:
			s.Unknown++
		}
	}
	s.Partial = s.Succeeded > 0 && (s.Unknown > 0 || s.Failed > 0 || s.Denied > 0)
	return s
}
