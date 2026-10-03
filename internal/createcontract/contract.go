// Package createcontract binds a new-file proposal to one workspace and one use.
// It never accepts an editcontract proposal or replacement permit.
package createcontract

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"strconv"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/netty-linux/daimon/internal/editcontract"
)

var (
	ErrInvalid    = errors.New("invalid creation contract")
	ErrLimit      = errors.New("creation proposal exceeds limits")
	ErrUnsafePath = errors.New("unsafe creation path")
	ErrFile       = errors.New("cannot inspect creation workspace")
	ErrConflict   = errors.New("creation target already exists")
	ErrChanged    = errors.New("creation target or parent changed")
	ErrDenied     = errors.New("creation proposal denied")
	ErrUsed       = errors.New("creation approval already attempted or consumed")
	ErrApproval   = errors.New("creation approval failed")
)

type Limits struct{ FinalBytes, Lines, PathBytes, PreviewBytes int }

func (l Limits) Validate() error {
	if l.FinalBytes <= 0 || l.FinalBytes > 1024*1024 || l.Lines <= 0 || l.Lines > 1000 || l.PathBytes <= 0 || l.PathBytes > 4096 || l.PreviewBytes <= 0 {
		return ErrInvalid
	}
	return nil
}

type Review struct {
	ID, RootID, Path, ProposedSHA256 string
	TargetAbsent                     bool
	Bytes                            int
	Limits                           Limits
	Display                          string
}
type Decision = editcontract.Decision

const (
	Deny  = editcontract.Deny
	Allow = editcontract.Allow
)

// Reviewer must display the complete detached Review and obtain an explicit,
// fresh decision. Incomplete display, EOF, invalid input and cancellation deny.
type Reviewer interface {
	Review(context.Context, Review) (Decision, error)
}

type Workspace struct {
	root *os.Root
	id   string
}

func Open(directory string) (*Workspace, error) {
	r, err := os.OpenRoot(directory)
	if err != nil {
		return nil, ErrFile
	}
	nonce := make([]byte, 16)
	if _, err = rand.Read(nonce); err != nil {
		r.Close()
		return nil, ErrFile
	}
	return &Workspace{root: r, id: hex.EncodeToString(nonce)}, nil
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
	content   []byte
	parents   []os.FileInfo
	attempted atomic.Bool
}
type Proposal struct{ state *proposalState }
type permitState struct {
	proposal    *proposalState
	approvalCtx context.Context
	used        atomic.Bool
}
type Permit struct{ state *permitState }

func (p *Permit) Invalidate() {
	if p != nil && p.state != nil {
		p.state.used.Store(true)
	}
}
func (p *Proposal) View() Review {
	if p == nil || p.state == nil {
		return Review{}
	}
	return p.state.review
}
func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func validPath(p string) bool {
	if !utf8.ValidString(p) || !fs.ValidPath(p) || p == "." || strings.ContainsAny(p, "\\:\x00<>\"|?*") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		name := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if name == "CON" || name == "PRN" || name == "AUX" || name == "NUL" || (len(name) == 4 && (strings.HasPrefix(name, "COM") || strings.HasPrefix(name, "LPT")) && name[3] >= '0' && name[3] <= '9') {
			return false
		}
	}
	return true
}

// Observe every parent without following symlinks, including the root identity.
func (w *Workspace) parents(ctx context.Context, p string) ([]os.FileInfo, error) {
	var infos []os.FileInfo
	names := []string{"."}
	dir := path.Dir(p)
	if dir != "." {
		prefix := ""
		for _, part := range strings.Split(dir, "/") {
			if prefix != "" {
				prefix += "/"
			}
			prefix += part
			names = append(names, prefix)
		}
	}
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := w.root.Lstat(name)
		if err != nil {
			return nil, ErrFile
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return nil, ErrUnsafePath
		}
		infos = append(infos, info)
	}
	return infos, nil
}
func (w *Workspace) absent(p string) error {
	_, err := w.root.Lstat(p)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return ErrFile
	}
	return nil
}
func (s *proposalState) revalidate(ctx context.Context, absent bool) error {
	infos, err := s.w.parents(ctx, s.review.Path)
	if err != nil {
		return err
	}
	if len(infos) != len(s.parents) {
		return ErrChanged
	}
	for i, info := range infos {
		if !os.SameFile(info, s.parents[i]) || info.Mode() != s.parents[i].Mode() {
			return ErrChanged
		}
	}
	if absent {
		if err := s.w.absent(s.review.Path); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (w *Workspace) Prepare(ctx context.Context, p string, content []byte, limits Limits) (*Proposal, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if w == nil || w.root == nil || limits.Validate() != nil {
		return nil, ErrInvalid
	}
	if !Supported() {
		return nil, ErrUnsupported
	}
	if !validPath(p) {
		return nil, ErrUnsafePath
	}
	if len(p) > limits.PathBytes || len(content) > limits.FinalBytes || strings.Count(string(content), "\n")+1 > limits.Lines {
		return nil, ErrLimit
	}
	if !utf8.Valid(content) {
		return nil, ErrInvalid
	}
	data := append([]byte(nil), content...)
	parents, err := w.parents(ctx, p)
	if err != nil {
		return nil, err
	}
	if err := w.absent(p); err != nil {
		return nil, err
	}
	r := Review{RootID: w.id, Path: p, TargetAbsent: true, Bytes: len(data), ProposedSHA256: digest(data), Limits: limits}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrInvalid
	}
	binding, _ := json.Marshal(struct {
		Nonce  string
		Review Review
	}{hex.EncodeToString(nonce), r})
	r.ID = digest(binding)
	r.Display = fmt.Sprintf("Proposta de criação: %s\nVínculo do workspace: %s\nCaminho: %s\nAlvo: AUSENTE na preparação; nunca sobrescrever. Todos os diretórios pais devem existir.\nProposto: sha256=%s bytes=%d\nLimites: final_bytes=%d lines=%d path_bytes=%d preview_bytes=%d\nCriação: somente Linux; criação exclusiva, permissões 0600. Workspace controlado obrigatório. Arquivo pode ficar visível durante a gravação. Limpeza após falha pode falhar; sem garantia contra perda de energia ou reversão após sucesso.\nCodificação: escapes ASCII de Go.\nConteúdo proposto (completo): %s\n", r.ID, r.RootID, strconv.QuoteToASCII(p), r.ProposedSHA256, r.Bytes, limits.FinalBytes, limits.Lines, limits.PathBytes, limits.PreviewBytes, strconv.QuoteToASCII(string(data)))
	if len(r.Display) > limits.PreviewBytes {
		return nil, ErrLimit
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &Proposal{state: &proposalState{w: w, review: r, content: data, parents: parents}}, nil
}
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
	if err := s.revalidate(ctx, true); err != nil {
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
	if err := s.revalidate(ctx, true); err != nil {
		return nil, err
	}
	return &Permit{state: &permitState{proposal: s, approvalCtx: ctx}}, nil
}
