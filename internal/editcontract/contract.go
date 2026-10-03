// Package editcontract prepares exact single-file proposals and applies approved
// replacements once. Apply supports Linux; read-only contracts remain portable.
package editcontract

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/diffview"
)

var (
	ErrInvalid    = errors.New("invalid edit contract configuration or input")
	ErrLimit      = errors.New("edit proposal exceeds limits")
	ErrUnsafePath = errors.New("unsafe edit path")
	ErrSymlink    = errors.New("edit path contains symlink")
	ErrFile       = errors.New("cannot inspect regular edit target")
	ErrChanged    = errors.New("edit target changed")
	ErrDenied     = errors.New("edit proposal denied")
	ErrUsed       = errors.New("edit approval already attempted or consumed")
	ErrApproval   = errors.New("edit approval failed")
	ErrHardLink   = errors.New("edit target has multiple hard links")
)

// Limits apply to each version and to the complete approval display.
// InputBytes and FinalBytes <= 1 MiB, Lines <= 1000 and PathBytes <= 4096 are required.
type Limits struct{ InputBytes, FinalBytes, Lines, PathBytes, PreviewBytes int }

func (l Limits) valid() bool {
	return l.InputBytes > 0 && l.InputBytes <= 1024*1024 && l.FinalBytes > 0 && l.FinalBytes <= 1024*1024 && l.Lines > 0 && l.Lines <= 1000 && l.PathBytes > 0 && l.PathBytes <= 4096 && l.PreviewBytes > 0
}
func (l Limits) Validate() error {
	if !l.valid() {
		return ErrInvalid
	}
	return nil
}

// Version describes bytes and metadata; file identity is also retained privately.
type Version struct {
	SHA256   string
	Bytes    int64
	Mode     uint32
	Modified string
}

// Review is a detached value. Display contains the complete escaped proposed
// content, not just the changed rows. A reviewer must display it in full.
type Review struct {
	ID, Path       string
	Original       Version
	ProposedSHA256 string
	Limits         Limits
	Display        string
}
type Decision uint8

const (
	Deny Decision = iota
	Allow
)

// Reviewer must obtain a fresh explicit decision for this exact Review. It must
// fail closed on incomplete display, EOF or invalid input and honor ctx.
type Reviewer interface {
	Review(context.Context, Review) (Decision, error)
}

// Workspace owns a root opened by the caller; it must outlive all proposals.
type Workspace struct{ root *os.Root }

func Open(directory string) (*Workspace, error) {
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrFile
	}
	return &Workspace{root: r}, nil
}
func (w *Workspace) Close() error {
	if w == nil || w.root == nil {
		return ErrInvalid
	}
	return w.root.Close()
}

type proposalState struct {
	w         *Workspace
	review    Review
	proposed  []byte
	info      os.FileInfo
	attempted atomic.Bool
}

// Proposal copies share lifecycle state; callers cannot mutate its binding.
type Proposal struct{ state *proposalState }
type permitState struct {
	proposal    *proposalState
	approvalCtx context.Context
	used        atomic.Bool
}

// Permit copies share one use, for either Consume (read-only) or Apply (write).
type Permit struct{ state *permitState }

// Invalidate abandons an unused approval, without reading or writing the target.
func (p *Permit) Invalidate() {
	if p != nil && p.state != nil {
		p.state.used.Store(true)
	}
}

type Validated struct {
	Review   Review
	Proposed []byte
}

func digest(data []byte) string { h := sha256.Sum256(data); return hex.EncodeToString(h[:]) }
func version(info os.FileInfo, data []byte) Version {
	return Version{SHA256: digest(data), Bytes: int64(len(data)), Mode: uint32(info.Mode()), Modified: info.ModTime().UTC().Format("2006-01-02T15:04:05.999999999Z07:00")}
}
func validPath(p string) bool {
	if !utf8.ValidString(p) || !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, "\\:\x00<>\"|?*") {
		return false
	}
	// Portable subset excludes Windows aliases and trailing-dot/space names.
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" ||
			(len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return false
		}
	}
	return true
}

// inspect refuses every symlink component, both internal and external. Root
// confines resolution; these checks do not eliminate hostile filesystem races.
func (w *Workspace) inspect(p string) (os.FileInfo, error) {
	prefix := ""
	for _, component := range strings.Split(p, "/") {
		prefix = path.Join(prefix, component)
		info, err := w.root.Lstat(prefix)
		if err != nil {
			return nil, ErrFile
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrSymlink
		}
		if prefix != p && !info.IsDir() {
			return nil, ErrFile
		}
		if prefix == p {
			if !info.Mode().IsRegular() {
				return nil, ErrFile
			}
			if err := checkLinks(info); err != nil {
				return nil, err
			}
			return info, nil
		}
	}
	return nil, ErrFile
}

func (w *Workspace) snapshot(ctx context.Context, p string, limit int) ([]byte, os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	before, err := w.inspect(p)
	if err != nil {
		return nil, nil, err
	}
	if before.Size() > int64(limit) {
		return nil, nil, ErrLimit
	}
	f, err := w.root.Open(p)
	if err != nil {
		return nil, nil, ErrFile
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() {
		return nil, nil, ErrFile
	}
	if err := checkLinks(opened); err != nil {
		return nil, nil, err
	}
	if !sameVersion(before, opened) {
		return nil, nil, ErrChanged
	}
	data, err := io.ReadAll(io.LimitReader(f, int64(limit)+1))
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if err != nil {
		return nil, nil, ErrFile
	}
	if len(data) > limit {
		return nil, nil, ErrLimit
	}
	after, err := f.Stat()
	if err != nil {
		return nil, nil, ErrFile
	}
	current, err := w.inspect(p)
	if err != nil {
		return nil, nil, err
	}
	if !sameVersion(opened, after) || !sameVersion(after, current) || int64(len(data)) != after.Size() {
		return nil, nil, ErrChanged
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	return data, after, nil
}
func sameVersion(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime()) && samePlatformMetadata(a, b)
}

func (w *Workspace) Prepare(ctx context.Context, p string, proposed []byte, limits Limits) (*Proposal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w == nil || w.root == nil || !limits.valid() {
		return nil, ErrInvalid
	}
	if !validPath(p) {
		return nil, ErrUnsafePath
	}
	if len(p) > limits.PathBytes || len(proposed) > limits.FinalBytes {
		return nil, ErrLimit
	}
	// Copy before reading the file or invoking any caller component.
	content := append([]byte(nil), proposed...)
	if !utf8.Valid(content) {
		return nil, ErrInvalid
	}
	original, info, err := w.snapshot(ctx, p, limits.InputBytes)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(original) {
		return nil, ErrInvalid
	}
	diff, err := diffview.Render(p, string(original), string(content), diffview.Limits{MaxLines: limits.Lines, MaxBytes: limits.PreviewBytes})
	if err != nil {
		if errors.Is(err, diffview.ErrTooLarge) {
			return nil, ErrLimit
		}
		return nil, ErrInvalid
	}
	r := Review{Path: p, Original: version(info, original), ProposedSHA256: digest(content), Limits: limits}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrInvalid
	}
	binding, _ := json.Marshal(struct {
		Nonce  string
		Review Review
	}{hex.EncodeToString(nonce), r})
	r.ID = digest(binding)
	// Full proposed content is quoted ASCII, including exact line endings.
	header := fmt.Sprintf("Proposta: %s\nCaminho: %s\nOriginal: sha256=%s bytes=%d mode=%04o modified=%s\nProposto: sha256=%s bytes=%d\nLimites: input_bytes=%d final_bytes=%d lines=%d path_bytes=%d preview_bytes=%d\nCodificação: escapes ASCII de Go; diff omite somente contexto inalterado.\nSubstituição: somente Linux; workspace controlado obrigatório. Escritores externos podem concorrer com a substituição. Cancelamento após confirmação não desfaz a substituição. Sem garantia contra perda de energia; proprietário/ACL/xattrs não são preservados.\n", r.ID, strconv.QuoteToASCII(p), r.Original.SHA256, r.Original.Bytes, r.Original.Mode, r.Original.Modified, r.ProposedSHA256, len(content), limits.InputBytes, limits.FinalBytes, limits.Lines, limits.PathBytes, limits.PreviewBytes)
	full := "Conteúdo proposto (completo): " + strconv.QuoteToASCII(string(content)) + "\n"
	if len(header) > limits.PreviewBytes || len(diff) > limits.PreviewBytes-len(header) || len(full) > limits.PreviewBytes-len(header)-len(diff) {
		return nil, ErrLimit
	}
	r.Display = header + diff + full
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Proposal{state: &proposalState{w: w, review: r, proposed: content, info: info}}, nil
}
func (p *Proposal) View() Review {
	if p == nil || p.state == nil {
		return Review{}
	}
	return p.state.review
}
func (s *proposalState) revalidate(ctx context.Context) error {
	data, info, err := s.w.snapshot(ctx, s.review.Path, s.review.Limits.InputBytes)
	if err != nil {
		return err
	}
	if !sameVersion(s.info, info) || version(info, data) != s.review.Original {
		return ErrChanged
	}
	return ctx.Err()
}

// Approve is attempted once. Denial, invalid decisions, errors and cancellation
// close the proposal; failures require preparing a fresh proposal.
func (p *Proposal) Approve(ctx context.Context, reviewer Reviewer) (*Permit, error) {
	if p == nil || p.state == nil {
		return nil, ErrInvalid
	}
	s := p.state
	if !s.attempted.CompareAndSwap(false, true) {
		return nil, ErrUsed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if reviewer == nil {
		return nil, ErrInvalid
	}
	if err := s.revalidate(ctx); err != nil {
		return nil, err
	}
	d, err := reviewer.Review(ctx, s.review)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, ErrApproval
	}
	if d != Allow {
		return nil, ErrDenied
	}
	if err := s.revalidate(ctx); err != nil {
		return nil, err
	}
	return &Permit{state: &permitState{proposal: s, approvalCtx: ctx}}, nil
}

// Consume irreversibly spends this permit before revalidation, including on
// failure/cancellation. Returned data is copied. It does not write any file and
// does not close the race between validation and a future executor's write.
func (p *Permit) Consume(ctx context.Context) (Validated, error) {
	if p == nil || p.state == nil {
		return Validated{}, ErrInvalid
	}
	if !p.state.used.CompareAndSwap(false, true) {
		return Validated{}, ErrUsed
	}
	if err := p.state.approvalCtx.Err(); err != nil {
		return Validated{}, err
	}
	s := p.state.proposal
	if err := s.revalidate(ctx); err != nil {
		return Validated{}, err
	}
	result := Validated{Review: s.review, Proposed: append([]byte(nil), s.proposed...)}
	if err := ctx.Err(); err != nil {
		return Validated{}, err
	}
	if err := p.state.approvalCtx.Err(); err != nil {
		return Validated{}, err
	}
	return result, nil
}
