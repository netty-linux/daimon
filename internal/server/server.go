// Package server transports local API requests. It owns neither agent execution
// nor the lifetime of injected stores, session contexts, or the Session Manager.
package server

import (
	"context"
	"errors"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/memory"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/sandbox"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	MaxBodyBytes     = 256 * 1024
	MaxMessageBytes  = 32 * 1024
	MaxResponseBytes = 4 * 1024 * 1024
	MaxEvents        = 256
)

type SessionManager interface {
	Start(context.Context, sessions.StartRequest) (sessions.Snapshot, error)
	Get(sessions.ID) (sessions.Snapshot, error)
	Abort(sessions.ID) error
	EventsSince(sessions.ID, uint64) (sessions.Replay, error)
}
type Dependencies struct {
	Environments *environments.Store
	Bots         *BotStore
	Threads      *ThreadStore
	Providers    *providers.Registry
	Sessions     SessionManager
	// Application-owned context, deliberately independent of HTTP request lifetime.
	SessionContext context.Context
	Conversations  ConversationReader
	// Explicit process capability exposure; neither a permission nor an HTTP field.
	EnableReplaceFile, EnableCreateFile bool
	Sandboxes                           *sandbox.Manager
	Computers                           *computer.Manager
	Memory                              *memory.Store
	MCP                                 MCPCatalog
}

type MCPCatalog interface {
	Servers() []mcp.ServerMetadata
	Tools() []mcp.ToolMetadata
}

type ConversationReader interface {
	List(context.Context, threads.ID) ([]conversations.Message, error)
}
type Server struct {
	referenceMu sync.Mutex // Serializes scoped-memory create/delete with target deletion.
	deps        Dependencies
	providerIDs []providers.ID
	http        *http.Server
	streams     chan struct{}
	stopping    chan struct{}
	stopOnce    sync.Once
	sse         sseOptions
	mediaMu     sync.Mutex
	mediaWG     sync.WaitGroup
}

var ErrConfig = errors.New("server: invalid configuration")
var ErrLifecycle = errors.New("server: lifecycle failure")

func New(deps Dependencies) (*Server, error) {
	if deps.Bots == nil || deps.Threads == nil || deps.Providers == nil || deps.Sessions == nil || deps.SessionContext == nil || (deps.EnableReplaceFile && deps.EnableCreateFile) {
		return nil, ErrConfig
	}
	s := &Server{deps: deps, providerIDs: deps.Providers.IDs(), streams: make(chan struct{}, MaxStreams), stopping: make(chan struct{}), sse: sseOptions{heartbeat: SSEHeartbeat, writeTimeout: SSEWriteTimeout, payloadBytes: MaxSSEPayloadBytes}}
	s.deps.Providers = nil
	s.http = &http.Server{Handler: http.HandlerFunc(s.localHTTP), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 16 * 1024, ErrorLog: log.New(io.Discard, "", 0)}
	return s, nil
}

// Serve accepts a caller-owned listener, verifies its actual TCP address, and
// blocks until Shutdown/Close. The caller closes listeners rejected here.
func (s *Server) Serve(l net.Listener) error {
	if l == nil {
		return ErrConfig
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	if !ok || addr.IP == nil || !addr.IP.IsLoopback() || addr.Zone != "" {
		return ErrConfig
	}
	err := s.http.Serve(l)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	if err != nil {
		return ErrLifecycle
	}
	return nil
}
func (s *Server) Shutdown(ctx context.Context) error {
	s.stopStreams()
	if err := s.http.Shutdown(ctx); err != nil {
		return ErrLifecycle
	}
	done := make(chan struct{})
	go func() { s.mediaMu.Lock(); s.mediaMu.Unlock(); s.mediaWG.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		return ErrLifecycle
	}
	return nil
}
func (s *Server) Close() error {
	s.stopStreams()
	if err := s.http.Close(); err != nil {
		return ErrLifecycle
	}
	return nil
}

func (s *Server) stopStreams() {
	s.stopOnce.Do(func() { s.mediaMu.Lock(); close(s.stopping); s.mediaMu.Unlock() })
}
