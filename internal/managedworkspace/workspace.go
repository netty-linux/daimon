// Package managedworkspace owns private, disposable copies, never source writes.
// The Linux boundary excludes other UIDs, not the owning UID or administrators.
package managedworkspace

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/netty-linux/daimon/internal/workspaceplan"
)

var (
	ErrUnsupported = errors.New("workspace gerenciado não suportado nesta plataforma")
	ErrPrivate     = errors.New("diretório privado de execução indisponível ou alterado")
	ErrImport      = errors.New("snapshot de origem recusada ou alterada")
	ErrLimit       = errors.New("limite de workspace gerenciado excedido")
	ErrState       = errors.New("execução gerenciada indisponível ou já utilizada")
	ErrArtifact    = errors.New("falha de artefato; efeitos precisam de revisão")
	ErrApply       = errors.New("aplicação interrompida; consulte o relatório privado")
)

const MaxFiles = 64
const MaxDirectories = 32
const MaxDepth = 8
const MaxFileBytes = 64 * 1024
const MaxTotalBytes = 1024 * 1024
const MaxPreviewBytes = 1024 * 1024

type Identity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}
type Manifest struct {
	Version         int       `json:"version"`
	RunID           string    `json:"run_id"`
	Created         time.Time `json:"created"`
	SourceReference string    `json:"source_reference_sha256"`
	Snapshot        string    `json:"snapshot_sha256"`
	Files           int       `json:"files"`
	Directories     int       `json:"directories"`
	Bytes           int       `json:"total_size_bytes"`
	Root            Identity  `json:"root_identity"`
	Output          Identity  `json:"output_identity"`
	Status          string    `json:"status"`
	Rules           string    `json:"rules"`
}
type OperationReport struct {
	Type         string `json:"type"`
	Path         string `json:"path"`
	BeforeSHA256 string `json:"before_sha256,omitempty"`
	AfterSHA256  string `json:"after_sha256"`
	Status       string `json:"status"`
}
type Report struct {
	Version    int               `json:"version"`
	Status     string            `json:"status"`
	Operations []OperationReport `json:"operations"`
}

type Run struct {
	mu                      sync.Mutex
	base, directory, output string
	root                    *os.Root
	manifest                Manifest
	closed                  bool
	lock, journalFile       *os.File
	active                  *os.File
	pending                 *Proposal
}

func Supported() bool { return supported() }
func (r *Run) ID() string {
	if r == nil {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manifest.RunID
}
func (r *Run) Manifest() Manifest {
	if r == nil {
		return Manifest{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.manifest
}
func (r *Run) Close() error {
	if r == nil {
		return ErrPrivate
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.active != nil {
		r.active.Close()
	}
	if r.pending != nil {
		r.pending.closePrepared()
	}
	if r.lock != nil {
		r.lock.Close()
	}
	if r.journalFile != nil {
		r.journalFile.Close()
	}
	if r.root == nil {
		return ErrPrivate
	}
	return r.root.Close()
}

func validID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !(c >= 'a' && c <= 'f' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Create reads a bounded snapshot before provisioning a new random run. Base
// is a store, never an output root. Source and base must not overlap.
func Create(ctx context.Context, base, source string) (*Run, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	baseAbs, err := canonicalBase(base)
	if err != nil {
		return nil, ErrPrivate
	}
	sourceAbs, err := filepath.Abs(source)
	if err != nil {
		return nil, ErrImport
	}
	sourceAbs, err = filepath.EvalSymlinks(sourceAbs)
	if err != nil {
		return nil, ErrImport
	}
	if baseAbs == sourceAbs || strings.HasPrefix(baseAbs, sourceAbs+string(os.PathSeparator)) || strings.HasPrefix(sourceAbs, baseAbs+string(os.PathSeparator)) {
		return nil, ErrPrivate
	}
	// Parent checks occur before creating the configured store.
	if err := checkAncestors(filepath.Dir(baseAbs)); err != nil {
		return nil, err
	}
	if err := checkLocalPath(filepath.Dir(baseAbs)); err != nil {
		return nil, err
	}
	if err := checkPhysicalOverlap(baseAbs, sourceAbs); err != nil {
		return nil, err
	}
	entries, files, dirs, total, snapshot, err := readSnapshot(ctx, sourceAbs, false)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(baseAbs, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, ErrPrivate
	}
	if err := checkPrivate(baseAbs); err != nil {
		return nil, err
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrPrivate
	}
	id := hex.EncodeToString(nonce)
	directory := filepath.Join(baseAbs, id)
	if err := os.Mkdir(directory, 0700); err != nil {
		return nil, ErrPrivate
	}
	output := filepath.Join(directory, "output")
	if err := os.Mkdir(output, 0700); err != nil {
		return nil, ErrPrivate
	}
	if err := os.Mkdir(filepath.Join(directory, "artifacts"), 0700); err != nil {
		return nil, ErrPrivate
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrPrivate
	}
	r := &Run{base: baseAbs, directory: directory, output: output, root: root}
	rootID, err := identity(directory)
	if err != nil {
		r.Close()
		return nil, err
	}
	outputID, err := identity(output)
	if err != nil {
		r.Close()
		return nil, err
	}
	r.manifest = Manifest{Version: 1, RunID: id, Created: time.Now().UTC(), SourceReference: workspaceplan.Hash([]byte(sourceAbs)), Snapshot: snapshot, Files: files, Directories: dirs, Bytes: total, Root: rootID, Output: outputID, Status: "importing", Rules: "private-linux-v1; source-read-only; no-symlinks-special-hardlinks; files-0600; directories-0700; no-publication"}
	if err := r.saveManifest(); err != nil {
		r.Close()
		return nil, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			r.manifest.Status = "import_failed"
			r.saveManifest()
			r.Close()
			return nil, err
		}
		name := "output/" + entry.Path
		if entry.Directory {
			err = r.root.Mkdir(name, 0700)
		} else {
			err = r.writeNew(name, entry.Data)
		}
		if err != nil {
			r.manifest.Status = "import_failed"
			r.saveManifest()
			r.Close()
			return nil, ErrImport
		}
	}
	_, _, _, _, copied, err := readSnapshot(ctx, output, true)
	if err != nil || copied != snapshot {
		r.manifest.Status = "import_failed"
		r.saveManifest()
		r.Close()
		return nil, ErrImport
	}
	r.manifest.Status = "ready"
	if err := r.saveManifest(); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// Base must be on the namespace root filesystem (checked by openat2). Thus
// inode comparison against its actual ancestors also detects source bind aliases.
func checkPhysicalOverlap(base, source string) error {
	i, err := os.Lstat(source)
	if err != nil || !i.IsDir() || i.Mode()&os.ModeSymlink != 0 {
		return ErrImport
	}
	for name := base; ; name = filepath.Dir(name) {
		ancestor, err := os.Lstat(name)
		if err == nil && os.SameFile(i, ancestor) {
			return ErrPrivate
		}
		if err != nil && !(name == base && errors.Is(err, os.ErrNotExist)) {
			return ErrPrivate
		}
		if filepath.Dir(name) == name {
			return nil
		}
	}
}

func Open(base, id string) (*Run, error) {
	if !Supported() {
		return nil, ErrUnsupported
	}
	if !validID(id) {
		return nil, ErrPrivate
	}
	base, err := canonicalBase(base)
	if err != nil {
		return nil, ErrPrivate
	}
	if err := checkPrivate(base); err != nil {
		return nil, err
	}
	directory := filepath.Join(base, id)
	if err := checkPrivate(directory); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrPrivate
	}
	r := &Run{base: base, directory: directory, output: filepath.Join(directory, "output"), root: root}
	data, err := r.readArtifact("manifest.json", 16384)
	if err != nil {
		r.Close()
		return nil, err
	}
	r.manifest, err = decodeManifest(data)
	if err != nil || r.manifest.RunID != id {
		r.Close()
		return nil, ErrPrivate
	}
	if err := r.check(); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

func (r *Run) check() error {
	if r.closed || r.root == nil {
		return ErrPrivate
	}
	for _, dir := range []string{r.base, r.directory, r.output, filepath.Join(r.directory, "artifacts")} {
		if err := checkPrivate(dir); err != nil {
			return err
		}
	}
	i, err := identity(r.directory)
	if err != nil || i != r.manifest.Root {
		return ErrPrivate
	}
	i, err = identity(r.output)
	if err != nil || i != r.manifest.Output {
		return ErrPrivate
	}
	opened, err := r.root.Stat(".")
	if err != nil || identityInfo(opened) != r.manifest.Root {
		return ErrPrivate
	}
	return nil
}

func (r *Run) writeNew(name string, data []byte) error {
	f, err := r.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ErrArtifact
	}
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return ErrArtifact
	}
	return r.syncDirectory(filepath.ToSlash(filepath.Dir(name)))
}
func (r *Run) syncDirectory(name string) error {
	f, err := r.root.Open(name)
	if err != nil {
		return ErrArtifact
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		return ErrArtifact
	}
	return nil
}
func (r *Run) saveManifest() error {
	data, err := json.Marshal(r.manifest)
	if err != nil {
		return ErrArtifact
	}
	// A new exclusive staging file is never reused after an interrupted save.
	if err := r.writeNew("manifest.next", append(data, '\n')); err != nil {
		return err
	}
	if err := r.root.Rename("manifest.next", "manifest.json"); err != nil {
		return ErrArtifact
	}
	return r.syncDirectory(".")
}
func (r *Run) readArtifact(name string, limit int64) ([]byte, error) {
	if err := checkPrivate(filepath.Join(r.directory, filepath.Dir(filepath.FromSlash(name)))); err != nil {
		return nil, ErrArtifact
	}
	i, err := r.root.Lstat(name)
	if err != nil || !safeRegular(i) || i.Size() > limit {
		return nil, ErrArtifact
	}
	f, err := r.root.Open(name)
	if err != nil {
		return nil, ErrArtifact
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(i, opened) {
		return nil, ErrArtifact
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, ErrArtifact
	}
	return data, nil
}

func (r *Run) Report() (Report, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.check(); err != nil {
		return Report{}, err
	}
	data, err := r.readArtifact("manifest.json", 16384)
	if err != nil {
		return Report{}, err
	}
	m, err := decodeManifest(data)
	if err != nil || m.RunID != r.manifest.RunID || m.Root != r.manifest.Root || m.Output != r.manifest.Output {
		return Report{}, ErrArtifact
	}
	r.manifest = m
	stagingAbsent, err := r.artifactAbsent("manifest.next")
	if err != nil {
		return Report{}, ErrArtifact
	}
	if !stagingAbsent {
		return Report{Version: 1, Status: "unknown_interrupted", Operations: []OperationReport{}}, nil
	}
	if r.manifest.Status == "ready" {
		absent, err := r.artifactAbsent("attempt.lock")
		if err != nil {
			return Report{}, err
		}
		if !absent {
			return Report{Version: 1, Status: "unknown_interrupted", Operations: []OperationReport{}}, nil
		}
		return Report{Version: 1, Status: "ready", Operations: []OperationReport{}}, nil
	}
	if r.manifest.Status == "prepared" || r.manifest.Status == "applying" {
		return Report{Version: 1, Status: "unknown_interrupted", Operations: []OperationReport{}}, nil
	}
	data, err = r.readArtifact("artifacts/report.json", 32768)
	if err != nil {
		return Report{}, err
	}
	var report Report
	if strictMetadata(data, &report) != nil || report.Version != 1 || len(report.Operations) > 2 || report.Status != r.manifest.Status {
		return Report{}, ErrArtifact
	}
	for _, op := range report.Operations {
		if !workspaceplan.ValidPath(op.Path, workspaceplan.DefaultLimits()) || !validHash(op.AfterSHA256) || (op.Type != "create_file" && op.Type != "replace_file") || (op.Type == "create_file" && op.BeforeSHA256 != "") || (op.Type == "replace_file" && !validHash(op.BeforeSHA256)) {
			return Report{}, ErrArtifact
		}
		switch op.Status {
		case "not_started", "succeeded", "denied", "unknown":
		default:
			return Report{}, ErrArtifact
		}
	}
	if err := r.verifyReport(report); err != nil {
		return Report{}, err
	}
	return report, nil
}

func validHash(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && s == strings.ToLower(s)
}
func decodeManifest(data []byte) (Manifest, error) {
	var m Manifest
	if strictMetadata(data, &m) != nil || m.Version != 1 || !validID(m.RunID) || !validHash(m.Snapshot) || !validHash(m.SourceReference) || m.Created.IsZero() || m.Files < 0 || m.Files > MaxFiles || m.Directories < 0 || m.Directories > MaxDirectories || m.Bytes < 0 || m.Bytes > MaxTotalBytes || m.Root.Inode == 0 || m.Output.Inode == 0 || m.Rules != "private-linux-v1; source-read-only; no-symlinks-special-hardlinks; files-0600; directories-0700; no-publication" {
		return Manifest{}, ErrArtifact
	}
	switch m.Status {
	case "ready", "importing", "import_failed", "prepared", "applying", "succeeded", "denied", "failed", "partial", "unknown":
	default:
		return Manifest{}, ErrArtifact
	}
	return m, nil
}
