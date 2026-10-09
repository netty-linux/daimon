package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/bots"
	"github.com/netty-linux/daimon/internal/computer"
	"github.com/netty-linux/daimon/internal/conversations"
	"github.com/netty-linux/daimon/internal/environments"
	"github.com/netty-linux/daimon/internal/mcp"
	"github.com/netty-linux/daimon/internal/memory"
	"github.com/netty-linux/daimon/internal/providers"
	"github.com/netty-linux/daimon/internal/routines"
	"github.com/netty-linux/daimon/internal/sandbox"
	"github.com/netty-linux/daimon/internal/server"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
	"io"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var errServe = errors.New("serve: configuration or lifecycle failure")

const serveShutdownTimeout = 10 * time.Second

func runServe(ctx context.Context, args []string, out io.Writer, getenv func(string) string) error {
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serveApplication(signalCtx, args, out, getenv, os.UserHomeDir, net.Listen)
}

// Application owns listener, stores and Manager. HTTP shuts down before workers
// are canceled/joined. A bounded shutdown cannot force uncooperative components.
func serveApplication(ctx context.Context, args []string, out io.Writer, getenv func(string) string, home func() (string, error), listen func(string, string) (net.Listener, error)) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	port := flags.Int("port", 3000, "loopback TCP port")
	dataDir := flags.String("data-dir", "", "private local state directory")
	mcpFile := flags.String("mcp-config", "", "explicit local MCP configuration")
	cloudExecutable := flags.String("cloud-cua", "", "explicit absolute CUA executable for official Fleet cloud guests")
	sandboxExecutable := flags.String("sandbox-cua", "", "explicit absolute CUA executable for disposable local Linux gVisor sandboxes")
	mediaURL := flags.String("computer-media-url", "", "optional existing cua-spacesd on the same local desktop")
	replace := flags.Bool("enable-replace-file", false, "expose replacement capability, still requiring one-shot approval")
	create := flags.Bool("enable-create-file", false, "expose creation capability, still requiring one-shot approval")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *port < 0 || *port > 65535 || (*replace && *create) {
		return errServe
	}
	// Reject repeated flags rather than silently accepting the last value.
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			name := strings.SplitN(strings.TrimLeft(arg, "-"), "=", 2)[0]
			if seen[name] {
				return errServe
			}
			seen[name] = true
			if !strings.Contains(arg, "=") && name != "enable-replace-file" && name != "enable-create-file" {
				i++
			}
		}
	}
	if seen["data-dir"] && strings.TrimSpace(*dataDir) == "" {
		return errServe
	}
	if seen["mcp-config"] && strings.TrimSpace(*mcpFile) == "" {
		return errServe
	}
	if ctx.Err() != nil {
		return errServe
	}
	if *dataDir == "" {
		root, err := home()
		if err != nil || root == "" {
			return errServe
		}
		*dataDir = filepath.Join(root, ".daimon")
	}
	dir, err := filepath.Abs(*dataDir)
	if err != nil {
		return errServe
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return errServe
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errServe
	}
	configPath := *mcpFile
	if configPath == "" {
		configPath = filepath.Join(dir, "mcp.json")
	}
	mcpConfig, err := mcp.LoadConfig(configPath, !seen["mcp-config"])
	if err != nil {
		return errServe
	}
	mcpManager, err := mcp.NewManager(ctx, mcpConfig, mcp.DefaultOptions(), func(ctx context.Context, serverID string) ([]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return mcpEnvironment(mcpConfig, serverID, getenv), nil
	})
	if err != nil {
		return errServe
	}
	defer func() {
		closeCtx, end := context.WithTimeout(context.Background(), serveShutdownTimeout)
		defer end()
		_ = mcpManager.Close(closeCtx)
	}()
	var backend computer.Backend
	if len(mcpManager.ComputerSources()) > 0 {
		backend, err = computer.NewCUA(mcpManager)
		if err != nil {
			return errServe
		}
	}
	computers, err := computer.NewManager(backend)
	if err != nil {
		return errServe
	}
	if seen["computer-media-url"] {
		media, mediaErr := computer.NewCUAMedia(*mediaURL, getenv("DAIMON_CUA_MEDIA_TOKEN"))
		if mediaErr != nil || computers.ConfigureMedia(media) != nil {
			return errServe
		}
	}
	defer func() {
		closeCtx, end := context.WithTimeout(context.Background(), serveShutdownTimeout)
		defer end()
		_ = computers.Close(closeCtx)
	}()
	var sandboxBackend sandbox.Backend = sandbox.UnavailableBackend{}
	if seen["sandbox-cua"] {
		stateDir := filepath.Join(dir, "cua-local")
		if os.Mkdir(stateDir, 0700) != nil {
			info, e := os.Lstat(stateDir)
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errServe
			}
		}
		env := []string{}
		for _, k := range []string{"PATH", "HOME", "XDG_RUNTIME_DIR", "TMPDIR", "LANG", "LC_ALL"} {
			if v := getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
		backend, e := sandbox.NewCUA(*sandboxExecutable, stateDir, env)
		if e != nil {
			return errServe
		}
		sandboxBackend = backend
	}
	var cloudBackend sandbox.Backend
	if seen["cloud-cua"] {
		stateDir := filepath.Join(dir, "cua-cloud")
		if os.Mkdir(stateDir, 0700) != nil {
			info, e := os.Lstat(stateDir)
			if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return errServe
			}
		}
		env := []string{}
		for _, k := range []string{"PATH", "HOME", "XDG_RUNTIME_DIR", "TMPDIR", "LANG", "LC_ALL", "FLEETS_TOKEN", "CUA_CLIENT_ID", "CUA_CLIENT_SECRET"} {
			if v := getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
		cloudBackend, err = sandbox.NewCloudCUA(*cloudExecutable, stateDir, env)
		if err != nil {
			return errServe
		}
	}
	sandboxes, err := sandbox.NewManagerWithCloud(filepath.Join(dir, "sandboxes.json"), sandboxBackend, cloudBackend, computers, sandbox.DefaultOptions())
	if err != nil {
		return errServe
	}
	// Failed recovery remains visible; never erase ownership or substitute host.
	reconcileCtx, reconcileEnd := context.WithTimeout(ctx, serveShutdownTimeout)
	_ = sandboxes.Reconcile(reconcileCtx)
	reconcileEnd()
	defer func() {
		closeCtx, end := context.WithTimeout(context.Background(), serveShutdownTimeout)
		defer end()
		_ = sandboxes.Close(closeCtx)
	}()
	botStore, err := bots.NewStore(filepath.Join(dir, "bots.json"))
	if err != nil {
		return errServe
	}
	threadStore, err := threads.NewStore(filepath.Join(dir, "threads.json"))
	if err != nil {
		return errServe
	}
	sharedBots, sharedThreads := server.WrapBots(botStore), server.WrapThreads(threadStore)
	conversationDir := filepath.Join(dir, "conversations")
	if err := os.MkdirAll(conversationDir, 0700); err != nil {
		return errServe
	}
	conversationStore, err := conversations.NewStore(conversationDir)
	if err != nil {
		return errServe
	}
	defer conversationStore.Close()
	memoryStore, err := memory.NewStore(filepath.Join(dir, "memory.json"))
	if err != nil {
		return errServe
	}
	defer memoryStore.Close()
	var environmentStore *environments.Store
	if environments.Supported() {
		environmentDir := filepath.Join(dir, "environments")
		if err := os.MkdirAll(environmentDir, 0700); err != nil {
			return errServe
		}
		environmentStore, err = environments.NewStore(environmentDir)
		if err != nil {
			return errServe
		}
		defer environmentStore.Close()
	}
	registry := &providers.Registry{}
	if registry.Register(providers.CompatibleFactory(providers.OpenAI)) != nil || registry.Register(providers.GroqFactory()) != nil {
		return errServe
	}
	key, base := getenv("DAIMON_API_KEY"), getenv("DAIMON_BASE_URL")
	sessionCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	manager, err := sessions.NewManager(sessions.Dependencies{Environments: environmentStore, Bots: sharedBots, Threads: sharedThreads, Providers: registry, Conversations: conversationStore, WebApprovals: true, MCP: mcpManager, Computers: computers, Sandboxes: sandboxes, Memory: memoryStore,
		Config: func(ctx context.Context, id providers.ID) (providers.Config, error) {
			if err := ctx.Err(); err != nil {
				return providers.Config{}, err
			}
			cfg := providers.Config{APIKey: key, MaxResponseBytes: maxResponseBytes}
			if id == providers.OpenAI {
				cfg.BaseURL = base
			}
			return cfg, nil
		},
	}, sessions.Options{Budget: agentloop.DefaultBudget(), EventCapacity: 1024, MaxSessions: 128, MaxMemoryContextBytes: memory.MaxContextBytes, MaxMemoryRecords: 16})
	if err != nil {
		return errServe
	}
	managerClosed := false
	routineStore, err := routines.Open(filepath.Join(dir, "routines.json"))
	if err != nil {
		return errServe
	}
	scheduler, err := routines.New(routineStore, manager, sharedThreads, sharedBots, *replace, *create)
	if err != nil {
		return errServe
	}
	defer func() {
		if managerClosed {
			return
		}
		closeCtx, end := context.WithTimeout(context.Background(), serveShutdownTimeout)
		defer end()
		_ = manager.Close(closeCtx)
	}()
	httpServer, err := server.New(server.Dependencies{Routines: scheduler, Environments: environmentStore, Bots: sharedBots, Threads: sharedThreads, Providers: registry, Sessions: manager, SessionContext: sessionCtx, Conversations: conversationStore, EnableReplaceFile: *replace, EnableCreateFile: *create, MCP: mcpManager, Computers: computers, Sandboxes: sandboxes, Memory: memoryStore})
	if err != nil {
		return errServe
	}
	listener, err := listen("tcp", "127.0.0.1:"+strconv.Itoa(*port))
	if err != nil {
		return errServe
	}
	defer listener.Close()
	routineCtx, stopRoutines := context.WithCancel(sessionCtx)
	routineDone := make(chan error, 1)
	go func() { routineDone <- scheduler.Run(routineCtx) }()
	routinesJoined := false
	defer func() {
		stopRoutines()
		if !routinesJoined {
			<-routineDone
		}
	}()
	done := make(chan error, 1)
	go func() { done <- httpServer.Serve(listener) }()
	if _, err := fmt.Fprintf(out, "DAIMON local HTTP: http://%s/\n", listener.Addr().String()); err != nil {
		_ = httpServer.Close()
		<-done
		return errServe
	}
	select {
	case err := <-done:
		if err != nil {
			return errServe
		}
		return nil
	case <-ctx.Done():
	case <-routineDone:
		routinesJoined = true
		_ = httpServer.Close()
		<-done
		return errServe
	}
	shutdownCtx, end := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer end()
	stopRoutines()
	httpErr := httpServer.Shutdown(shutdownCtx)
	if httpErr != nil {
		_ = httpServer.Close()
	}
	serveErr := <-done
	var routineErr error
	select {
	case routineErr = <-routineDone:
	case <-shutdownCtx.Done():
		routineErr = errServe
	}
	routinesJoined = true
	// Same explicit overall deadline bounds HTTP draining plus Manager joining.
	mediaErr := computers.StopMedia(shutdownCtx)
	managerErr := manager.Close(shutdownCtx)
	managerClosed = true
	cancel()
	sandboxErr := sandboxes.Close(shutdownCtx)
	computerErr := computers.Close(shutdownCtx)
	mcpErr := mcpManager.Close(shutdownCtx)
	if routineErr != nil || httpErr != nil || serveErr != nil || mediaErr != nil || managerErr != nil || sandboxErr != nil || computerErr != nil || mcpErr != nil {
		return errServe
	}
	return nil
}

// Operational variables only. CUA additionally needs the existing graphical
// session/authentication and per-user OS directories; generic MCP stays unchanged.
func mcpEnvironment(config mcp.Config, serverID string, getenv func(string) string) []string {
	names := []string{"PATH", "SystemRoot", "WINDIR", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL"}
	for _, s := range config.Servers {
		if s.ID == serverID && s.ComputerBackend == string(computer.CUALocal) {
			names = append(names, "DISPLAY", "WAYLAND_DISPLAY", "XAUTHORITY", "XDG_RUNTIME_DIR", "DBUS_SESSION_BUS_ADDRESS", "HOME", "USERPROFILE", "APPDATA", "LOCALAPPDATA")
			break
		}
	}
	values := []string{}
	for _, name := range names {
		if value := getenv(name); value != "" {
			values = append(values, name+"="+value)
		}
	}
	return values
}
