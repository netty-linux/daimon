package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/netty-linux/daimon/internal/agentloop"
	"github.com/netty-linux/daimon/internal/model"
	"github.com/netty-linux/daimon/internal/sessions"
	"github.com/netty-linux/daimon/internal/threads"
)

type sseFrame struct{ id, name, data, retry, comment string }

func readFrame(reader *bufio.Reader) (sseFrame, error) {
	frame := sseFrame{}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return frame, err
		}
		line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if line == "" {
			return frame, nil
		}
		if strings.HasPrefix(line, ":") {
			frame.comment = strings.TrimSpace(line[1:])
			continue
		}
		key, value, _ := strings.Cut(line, ":")
		value = strings.TrimPrefix(value, " ")
		switch key {
		case "id":
			frame.id = value
		case "event":
			frame.name = value
		case "data":
			frame.data = value
		case "retry":
			frame.retry = value
		}
	}
}
func parseFrames(t *testing.T, text string) []sseFrame {
	t.Helper()
	reader := bufio.NewReader(strings.NewReader(text))
	frames := []sseFrame{}
	for {
		frame, err := readFrame(reader)
		if err == io.EOF {
			return frames
		}
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
}
func assertPrivate(t *testing.T, text string) {
	t.Helper()
	for _, private := range []string{"private-", "workspace", "instructions", "arguments", "APIKey", "FinalText"} {
		if strings.Contains(text, private) {
			t.Fatalf("private metadata in SSE: %s", text)
		}
	}
}

// Recorder with deadline support exercises handler framing/flush without a socket.
type streamRecorder struct {
	*httptest.ResponseRecorder
	flushes  int
	deadline time.Time
	fail     bool
}

func (w *streamRecorder) SetWriteDeadline(deadline time.Time) error {
	w.deadline = deadline
	return nil
}
func (w *streamRecorder) Flush() { w.flushes++; w.ResponseRecorder.Flush() }
func (w *streamRecorder) Write(data []byte) (int, error) {
	if w.fail {
		return 0, errors.New("private-write-error")
	}
	return w.ResponseRecorder.Write(data)
}
func streamRequest(s *Server, path string, headers http.Header) *streamRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if headers != nil {
		r.Header = headers
	}
	w := &streamRecorder{ResponseRecorder: httptest.NewRecorder()}
	s.ServeHTTP(w, r)
	return w
}
func TestSSEReplayFramingCursorsAndGap(t *testing.T) {
	for _, capacity := range []int{2, 64} {
		t.Run(strconv.Itoa(capacity), func(t *testing.T) {
			f := setup(t, finalModel, capacity)
			expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", "thread", "private-prompt", ""}), 202)
			wait(t, f, "session")
			for _, cursor := range []struct {
				query, header string
				after         uint64
			}{{"", "", 0}, {"?after=2", "", 2}, {"", "2", 2}, {"?after=0", "2", 2}, {"?after=invalid", "2", 2}, {"?after=18446744073709551615", "", ^uint64(0)}} {
				headers := http.Header{}
				if cursor.header != "" {
					headers.Set("Last-Event-ID", cursor.header)
				}
				w := streamRequest(f.server, "/api/v1/sessions/session/events/stream"+cursor.query, headers)
				if w.Code != 200 || w.Header().Get("Content-Type") != "text/event-stream" || w.Header().Get("Cache-Control") != "no-cache" {
					t.Fatal(w.Code, w.Header())
				}
				assertPrivate(t, w.Body.String())
				frames := parseFrames(t, w.Body.String())
				if len(frames) < 2 || frames[0].retry != "3000" || frames[len(frames)-1].name != "stream_end" {
					t.Fatal(frames)
				}
				replay, _ := f.manager.EventsSince("session", cursor.after)
				count := 0
				gaps := 0
				previous := cursor.after
				for _, frame := range frames[1:] {
					if frame.name == "replay_gap" {
						gaps++
						if frame.id != "" || count != 0 || !json.Valid([]byte(frame.data)) {
							t.Fatal("gap framing")
						}
						continue
					}
					if frame.name == "stream_end" {
						if frame.id != "" {
							t.Fatal("artificial id")
						}
						continue
					}
					seq, err := strconv.ParseUint(frame.id, 10, 64)
					if err != nil || seq <= previous {
						t.Fatal("sequence")
					}
					previous = seq
					var payload eventView
					if json.Unmarshal([]byte(frame.data), &payload) != nil || payload.Sequence != seq || payload.Kind != frame.name {
						t.Fatal("payload")
					}
					count++
				}
				if count != len(replay.Events) || (gaps == 1) != replay.Gap || w.flushes != len(frames) {
					t.Fatal("replay/flush mismatch")
				}
			}
		})
	}
}

func TestSSEValidationBeforeStream(t *testing.T) {
	f := setup(t, finalModel, 10)
	expect(t, request(f.server, "POST", "/api/v1/sessions/session/events/stream", nil), 405)
	expect(t, request(f.server, "GET", "/api/v1/sessions/missing/events/stream", nil), 404)
	for _, query := range []string{"?after=-1", "?after=", "?after=1&after=2", "?after=18446744073709551616", "?after=%zz", "?other=1"} {
		expect(t, request(f.server, "GET", "/api/v1/sessions/session/events/stream"+query, nil), 400)
	}
	for _, value := range []string{"", "-1", "1,2", "1\n2", "18446744073709551616"} {
		r := httptest.NewRequest("GET", "/api/v1/sessions/session/events/stream", nil)
		r.Header.Set("Last-Event-ID", value)
		w := httptest.NewRecorder()
		f.server.ServeHTTP(w, r)
		expect(t, w, 400)
	}
	r := httptest.NewRequest("GET", "/api/v1/sessions/session/events/stream", nil)
	r.Header["Last-Event-Id"] = []string{"1", "2"}
	w := httptest.NewRecorder()
	f.server.ServeHTTP(w, r)
	expect(t, w, 400)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", "thread", "prompt", ""}), 202)
	wait(t, f, "session")
	// Flusher alone is insufficient to bound writes: unsupported controller is safe.
	expect(t, request(f.server, "GET", "/api/v1/sessions/session/events/stream", nil), 500)
	unsupported := &noFlushWriter{header: http.Header{}}
	f.server.ServeHTTP(unsupported, httptest.NewRequest("GET", "/api/v1/sessions/session/events/stream", nil))
	if unsupported.status != 500 {
		t.Fatal("unflushable accepted")
	}
}

type noFlushWriter struct {
	header http.Header
	status int
}

func (w *noFlushWriter) Header() http.Header         { return w.header }
func (w *noFlushWriter) WriteHeader(status int)      { w.status = status }
func (w *noFlushWriter) Write(p []byte) (int, error) { return len(p), nil }

func localSSE(t *testing.T, f *fixture) (string, *http.Client) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- f.server.Serve(listener) }()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{Proxy: nil}}
	t.Cleanup(func() {
		client.CloseIdleConnections()
		_ = f.server.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not finish")
		}
	})
	return "http://" + listener.Addr().String(), client
}
func connect(t *testing.T, client *http.Client, url, header string) (*http.Response, *bufio.Reader) {
	t.Helper()
	r, err := http.NewRequest("GET", url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if header != "" {
		r.Header.Set("Last-Event-ID", header)
	}
	response, err := client.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	frame, err := readFrame(reader)
	if err != nil || frame.retry != "3000" {
		t.Fatal("initial flush", frame, err)
	}
	return response, reader
}
func nextFrame(t *testing.T, r *bufio.Reader) sseFrame {
	t.Helper()
	frame, err := readFrame(r)
	if err != nil {
		t.Fatal(err)
	}
	assertPrivate(t, frame.data)
	return frame
}

func TestSSERealLoopbackDisconnectReconnectAndMultipleClients(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	f := setup(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		close(entered)
		select {
		case <-release:
			return model.ModelResponse{FinalText: "private-result"}, nil
		case <-ctx.Done():
			return model.ModelResponse{}, ctx.Err()
		}
	}, 64)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", "thread", "private-prompt", ""}), 202)
	<-entered
	base, client := localSSE(t, f)
	endpoint := base + "/api/v1/sessions/session/events/stream"
	first, reader := connect(t, client, endpoint, "")
	a := nextFrame(t, reader)
	b := nextFrame(t, reader)
	if a.id != "1" || b.id != "2" {
		t.Fatal("initial order")
	}
	first.Body.Close()
	snapshot, _ := f.manager.Get("session")
	if snapshot.Status != sessions.Running {
		t.Fatal("disconnect aborted session")
	}
	// Another independent observer starts from its own cursor before the new event.
	second, reader2 := connect(t, client, endpoint+"?after=1", "")
	if nextFrame(t, reader2).id != "2" {
		t.Fatal("independent cursor")
	}
	third, reader3 := connect(t, client, endpoint, "2")
	close(release)
	wait(t, f, "session")
	for _, r := range []*bufio.Reader{reader2, reader3} {
		last := uint64(2)
		for {
			frame := nextFrame(t, r)
			if frame.name == "stream_end" {
				break
			}
			seq, err := strconv.ParseUint(frame.id, 10, 64)
			if err != nil || seq != last+1 {
				t.Fatal("missed/duplicated event")
			}
			last = seq
		}
		if _, err := readFrame(r); err != io.EOF {
			t.Fatal("terminal stream did not close", err)
		}
	}
	second.Body.Close()
	third.Body.Close()
	// Reconnect after completed execution with an old ID replays missed events.
	fourth, r4 := connect(t, client, endpoint, "2")
	defer fourth.Body.Close()
	if nextFrame(t, r4).id != "3" {
		t.Fatal("reconnect failed")
	}
	for nextFrame(t, r4).name != "stream_end" {
	}
	if _, err := readFrame(r4); err != io.EOF {
		t.Fatal(err)
	}
}

type countingManager struct {
	*sessions.Manager
	calls    atomic.Int32
	observed chan struct{}
	once     sync.Once
}

func (m *countingManager) EventsSince(id sessions.ID, after uint64) (sessions.Replay, error) {
	m.calls.Add(1)
	m.once.Do(func() { close(m.observed) })
	return m.Manager.EventsSince(id, after)
}
func TestSSEHeartbeatNoPollingAndShutdown(t *testing.T) {
	entered := make(chan struct{})
	f := setup(t, func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
		close(entered)
		<-ctx.Done()
		return model.ModelResponse{}, ctx.Err()
	}, 64)
	expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", "thread", "prompt", ""}), 202)
	<-entered
	counted := &countingManager{Manager: f.manager, observed: make(chan struct{})}
	f.server.deps.Sessions = counted
	f.server.sse.heartbeat = 100 * time.Millisecond
	f.server.sse.writeTimeout = 50 * time.Millisecond
	before, _ := f.manager.EventsSince("session", 0)
	base, client := localSSE(t, f)
	response, reader := connect(t, client, base+"/api/v1/sessions/session/events/stream?after=2", "")
	defer response.Body.Close()
	<-counted.observed
	for n := 0; n < 3; n++ {
		frame := nextFrame(t, reader)
		if frame.comment != "keep-alive" || frame.id != "" {
			t.Fatal("heartbeat")
		}
	}
	after, _ := f.manager.EventsSince("session", 0)
	if before.LastSequence != after.LastSequence || counted.calls.Load() != 1 {
		t.Fatal("heartbeat polled/mutated runtime")
	}
	ctx, end := context.WithTimeout(context.Background(), 5*time.Second)
	defer end()
	if err := f.server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := readFrame(reader); err != io.EOF {
		t.Fatal("shutdown failed", err)
	}
	snapshot, _ := f.manager.Get("session")
	if snapshot.Status != sessions.Running {
		t.Fatal("HTTP shutdown closed Manager")
	}
	if len(f.server.streams) != 0 {
		t.Fatal("slot leaked")
	}
}

type fixedObserver struct {
	SessionManager
	observation sessions.EventObservation
	replay      sessions.Replay
	failure     error
}

func (m *fixedObserver) ObserveEvents(sessions.ID) (sessions.EventObservation, error) {
	return m.observation, nil
}
func (m *fixedObserver) EventsSince(sessions.ID, uint64) (sessions.Replay, error) {
	return m.replay, m.failure
}
func TestSSEPayloadNamesAndWriteFailure(t *testing.T) {
	for _, kind := range []string{"payload", "name", "stop", "error", "write"} {
		t.Run(kind, func(t *testing.T) {
			f := setup(t, finalModel, 10)
			event := sessions.Event{Sequence: 1, Event: agentloop.Event{Kind: agentloop.LoopStarted}}
			fake := &fixedObserver{observation: sessions.EventObservation{Status: sessions.Completed, Terminal: true}, replay: sessions.Replay{Events: []sessions.Event{event}, LastSequence: 1}}
			switch kind {
			case "payload":
				f.server.sse.payloadBytes = 40
			case "name":
				fake.replay.Events[0].Event.Kind = "private-name\n\x00"
			case "stop":
				fake.replay.Events[0].Event.StopReason = "private-stop"
			case "error":
				fake.failure = errors.New("private-error")
			}
			f.server.deps.Sessions = fake
			w := &streamRecorder{ResponseRecorder: httptest.NewRecorder(), fail: kind == "write"}
			f.server.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/sessions/session/events/stream", nil))
			assertPrivate(t, w.Body.String())
			if kind != "write" && !strings.Contains(w.Body.String(), "event: transport_error") {
				t.Fatal("no controlled transport error")
			}
			if len(f.server.streams) != 0 {
				t.Fatal("slot leaked")
			}
		})
	}
}

func TestSSEStreamCapacityAndRelease(t *testing.T) {
	f := setup(t, finalModel, 10)
	// Fill all real admission slots with waiting handlers, no per-client queue.
	changed := make(chan struct{})
	f.server.deps.Sessions = &fixedObserver{observation: sessions.EventObservation{Changed: changed, Status: sessions.Running}}
	base, client := localSSE(t, f)
	responses := []*http.Response{}
	for n := 0; n < MaxStreams; n++ {
		response, _ := connect(t, client, base+"/api/v1/sessions/session/events/stream", "")
		responses = append(responses, response)
	}
	response, err := client.Get(base + "/api/v1/sessions/session/events/stream")
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != 429 || !strings.Contains(string(data), "stream_capacity") {
		t.Fatal(response.StatusCode, string(data))
	}
	ctx, end := context.WithTimeout(context.Background(), 5*time.Second)
	defer end()
	if err := f.server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	for _, response := range responses {
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
	if len(f.server.streams) != 0 {
		t.Fatal("slots leaked")
	}
}

func TestSSETerminalFailedAbortedAndStartup(t *testing.T) {
	for _, state := range []string{"failed", "aborted", "startup"} {
		t.Run(state, func(t *testing.T) {
			generate := modelFunc(func(context.Context, model.ModelRequest) (model.ModelResponse, error) {
				return model.ModelResponse{}, errors.New("private-failure")
			})
			entered := make(chan struct{})
			if state == "aborted" {
				generate = func(ctx context.Context, _ model.ModelRequest) (model.ModelResponse, error) {
					close(entered)
					<-ctx.Done()
					return model.ModelResponse{}, ctx.Err()
				}
			}
			f := setup(t, generate, 64)
			thread := "thread"
			if state == "startup" {
				thread = "missing"
			}
			expect(t, request(f.server, "POST", "/api/v1/sessions", startRequest{"session", threads.ID(thread), "prompt", ""}), 202)
			if state == "aborted" {
				<-entered
				if err := f.manager.Abort("session"); err != nil {
					t.Fatal(err)
				}
			}
			snapshot := wait(t, f, "session")
			w := streamRequest(f.server, "/api/v1/sessions/session/events/stream", nil)
			assertPrivate(t, w.Body.String())
			frames := parseFrames(t, w.Body.String())
			last := frames[len(frames)-1]
			if last.name != "stream_end" || !strings.Contains(last.data, string(snapshot.Status)) {
				t.Fatal("terminal status")
			}
		})
	}
}
