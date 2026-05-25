package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anutron/plannotator_argus/internal/argus"
)

// recorder synchronizes test-side reads with handler-side writes so the
// race detector doesn't flag the obvious slice append from inside an
// httptest handler.
type recorder struct {
	mu      sync.Mutex
	posts   []string
	deletes []string
}

func (r *recorder) addPost(s string)   { r.mu.Lock(); r.posts = append(r.posts, s); r.mu.Unlock() }
func (r *recorder) addDelete(s string) { r.mu.Lock(); r.deletes = append(r.deletes, s); r.mu.Unlock() }
func (r *recorder) postCount() int     { r.mu.Lock(); defer r.mu.Unlock(); return len(r.posts) }
func (r *recorder) deleteSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.deletes))
	copy(out, r.deletes)
	return out
}

func TestRegistrarRegistersOnStart(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.addPost(r.URL.Path)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(time.Hour) // disable heartbeat for this test
	r.Add(ToolDefinition{Name: "plannotator_a"})
	r.Add(ToolDefinition{Name: "plannotator_b"})

	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer r.Stop(context.Background())
	if rec.postCount() != 2 {
		t.Errorf("registered %d, want 2", rec.postCount())
	}
}

func TestRegistrarFailsFastOn401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := argus.New(srv.URL, "bad")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.Add(ToolDefinition{Name: "plannotator_a"})
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("expected error from 401")
	}
}

func TestRegistrarHeartbeatRePOSTs(t *testing.T) {
	var posts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts.Add(1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(50 * time.Millisecond)
	r.Add(ToolDefinition{Name: "plannotator_a"})

	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	r.Stop(context.Background())

	got := posts.Load()
	if got < 3 {
		t.Errorf("posts = %d, want >= 3 (initial + 2 heartbeats)", got)
	}
}

func TestRegistrarUnregistersOnStop(t *testing.T) {
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			rec.addDelete(r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(time.Hour)
	r.Add(ToolDefinition{Name: "plannotator_a"})
	r.Add(ToolDefinition{Name: "plannotator_b"})

	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.Stop(context.Background())
	if got := rec.deleteSnapshot(); len(got) != 2 {
		t.Errorf("deletes = %v, want 2", got)
	}
}

func TestRegistrarStopIdempotent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	client := argus.New(srv.URL, "tok")
	r := NewRegistrar(client, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(time.Hour)
	r.Add(ToolDefinition{Name: "plannotator_a"})
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Second Stop must not panic or block.
	if err := r.Stop(context.Background()); err != nil {
		t.Errorf("second Stop returned err: %v", err)
	}
}

// --- Heartbeat failure classification tests ---------------------------------
//
// These tests cover the nine scenarios in the "Argus link liveness detection"
// requirement. They share a programmable fake http.RoundTripper that returns
// a configured response or transport error per request.

// fakeResponse describes one canned HTTP response or transport error.
type fakeResponse struct {
	err    error
	status int
	body   string
}

// fakeTransport returns canned responses or transport errors in FIFO order.
// When the queue empties, subsequent requests fall back to a "default"
// response so background heartbeat ticks after a recovery do not spuriously
// fail.
type fakeTransport struct {
	mu      sync.Mutex
	queue   []fakeResponse
	defResp fakeResponse
	calls   atomic.Int32
}

func newFakeTransport(def fakeResponse) *fakeTransport {
	return &fakeTransport{defResp: def}
}

func (f *fakeTransport) enqueue(rs ...fakeResponse) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queue = append(f.queue, rs...)
}

func (f *fakeTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	f.calls.Add(1)
	f.mu.Lock()
	var r fakeResponse
	if len(f.queue) > 0 {
		r = f.queue[0]
		f.queue = f.queue[1:]
	} else {
		r = f.defResp
	}
	f.mu.Unlock()
	if r.err != nil {
		return nil, r.err
	}
	resp := &http.Response{
		StatusCode: r.status,
		Body:       io.NopCloser(strings.NewReader(r.body)),
		Header:     make(http.Header),
		Request:    req,
	}
	return resp, nil
}

// makeFakeClient builds an argus.Client whose underlying HTTP uses the given
// fake transport. BaseURL is a dummy value the transport ignores.
func makeFakeClient(ft *fakeTransport) *argus.Client {
	c := argus.New("http://fake.invalid", "tok")
	c.HTTP = &http.Client{Transport: ft, Timeout: 2 * time.Second}
	return c
}

// connRefusedErr is a sentinel transport-level error used in tests.
var connRefusedErr = errors.New("dial tcp 127.0.0.1:1: connect: connection refused")

// drainFatal blocks up to d for a value on r.Fatal() and returns it (nil on timeout).
func drainFatal(t *testing.T, r *Registrar, d time.Duration) error {
	t.Helper()
	select {
	case err := <-r.Fatal():
		return err
	case <-time.After(d):
		return nil
	}
}

// newTestRegistrar wires a registrar against a fake client with short
// heartbeat (50ms) and short fast-retry (50ms) for fast tests.
func newTestRegistrar(t *testing.T, ft *fakeTransport, defs ...string) *Registrar {
	t.Helper()
	c := makeFakeClient(ft)
	r := NewRegistrar(c, "http://127.0.0.1:7745", "Bearer secret", nil)
	r.SetHeartbeat(50 * time.Millisecond)
	r.SetFastRetry(50 * time.Millisecond)
	if len(defs) == 0 {
		defs = []string{"plannotator_a"}
	}
	for _, n := range defs {
		r.Add(ToolDefinition{Name: n})
	}
	return r
}

// Scenario 1: Successful heartbeat resets failure tracking.
//
// Arrange a transport failure for the first heartbeat (after the initial
// registration), then a success on the fast retry. After the success, no
// fatal should fire even after several more heartbeat intervals pass, and
// internal state should be cleared.
func TestHeartbeatSuccessResetsFailureState(t *testing.T) {
	ft := newFakeTransport(fakeResponse{status: 200})
	// Initial Start: 200 (queued).
	// First heartbeat tick: transport error (queued).
	// Fast-retry: falls back to default 200 → recovery.
	ft.enqueue(
		fakeResponse{status: 200},
		fakeResponse{err: connRefusedErr},
	)
	r := newTestRegistrar(t, ft)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop(context.Background())

	// Wait long enough for: heartbeat tick (50ms) → transport fail →
	// fast-retry (50ms) → success → multiple subsequent heartbeats.
	if fatal := drainFatal(t, r, 600*time.Millisecond); fatal != nil {
		t.Errorf("unexpected fatal: %v", fatal)
	}
	if pending := r.pendingFastRetry(); pending {
		t.Errorf("fast-retry timer still pending after recovery")
	}
	if cnt := r.consecutiveTransportFailures(); cnt != 0 {
		t.Errorf("consecutive transport failures = %d, want 0", cnt)
	}
}

// Scenario 2: First transport failure schedules a fast retry (does not exit).
func TestFirstTransportFailureSchedulesFastRetryNotFatal(t *testing.T) {
	ft := newFakeTransport(fakeResponse{status: 200})
	ft.enqueue(
		fakeResponse{status: 200},         // initial Start
		fakeResponse{err: connRefusedErr}, // first heartbeat tick
	)
	r := newTestRegistrar(t, ft)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop(context.Background())

	if fatal := drainFatal(t, r, 200*time.Millisecond); fatal != nil {
		t.Errorf("unexpected fatal on first transport failure: %v", fatal)
	}
}

// Scenario 3: Second consecutive transport failure is fatal.
func TestSecondTransportFailureIsFatal(t *testing.T) {
	// After initial 200, every subsequent call is a transport error.
	ft := newFakeTransport(fakeResponse{err: connRefusedErr})
	ft.enqueue(fakeResponse{status: 200}) // initial Start
	r := newTestRegistrar(t, ft)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop(context.Background())

	fatal := drainFatal(t, r, 1*time.Second)
	if fatal == nil {
		t.Fatal("expected fatal after second transport failure, got nil")
	}
}

// Scenario 4: HTTP 401 from a heartbeat is immediately fatal (no fast retry).
func TestHeartbeat401IsImmediatelyFatal(t *testing.T) {
	ft := newFakeTransport(fakeResponse{status: 401})
	ft.enqueue(fakeResponse{status: 200}) // initial Start
	r := newTestRegistrar(t, ft)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop(context.Background())

	start := time.Now()
	fatal := drainFatal(t, r, 1*time.Second)
	if fatal == nil {
		t.Fatal("expected fatal from 401, got nil")
	}
	if !errors.Is(fatal, argus.ErrUnauthorized) {
		t.Errorf("fatal = %v, want errors.Is(...ErrUnauthorized)", fatal)
	}
	// Must not have taken a fast-retry (50ms) wait beyond the heartbeat tick.
	if elapsed := time.Since(start); elapsed > 400*time.Millisecond {
		t.Errorf("401 fatal took %v, expected ~heartbeat interval (no fast-retry)", elapsed)
	}
}

// Scenario 5: HTTP 5xx is a warning, not fatal; no fast retry; no counter bump.
func TestHeartbeat5xxIsWarningNotFatal(t *testing.T) {
	ft := newFakeTransport(fakeResponse{status: 500, body: `{"err":"oops"}`})
	ft.enqueue(fakeResponse{status: 200}) // initial Start
	r := newTestRegistrar(t, ft)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop(context.Background())

	if fatal := drainFatal(t, r, 400*time.Millisecond); fatal != nil {
		t.Errorf("unexpected fatal on 5xx: %v", fatal)
	}
	if pending := r.pendingFastRetry(); pending {
		t.Errorf("5xx scheduled a fast-retry timer")
	}
	if cnt := r.consecutiveTransportFailures(); cnt != 0 {
		t.Errorf("5xx bumped transport failure counter to %d", cnt)
	}
}

// Scenario 6: HTTP non-401 4xx is a warning, not fatal.
func TestHeartbeatNon401_4xxIsWarningNotFatal(t *testing.T) {
	ft := newFakeTransport(fakeResponse{status: 403, body: `{"err":"forbidden"}`})
	ft.enqueue(fakeResponse{status: 200}) // initial Start
	r := newTestRegistrar(t, ft)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop(context.Background())

	if fatal := drainFatal(t, r, 400*time.Millisecond); fatal != nil {
		t.Errorf("unexpected fatal on 4xx: %v", fatal)
	}
	if pending := r.pendingFastRetry(); pending {
		t.Errorf("4xx scheduled a fast-retry timer")
	}
	if cnt := r.consecutiveTransportFailures(); cnt != 0 {
		t.Errorf("4xx bumped transport failure counter to %d", cnt)
	}
}

// Scenario 7: Recovery from a single transport failure (fast-retry succeeds).
//
// After a transport failure on heartbeat N, the fast-retry succeeds. We must
// resume the normal cadence and clear all failure state. A subsequent
// transport failure on heartbeat N+M should once again be treated as "first"
// failure (warn + schedule fast retry), NOT as the second consecutive.
func TestRecoveryClearsFailureState(t *testing.T) {
	ft := newFakeTransport(fakeResponse{status: 200})
	ft.enqueue(
		fakeResponse{status: 200},         // initial Start
		fakeResponse{err: connRefusedErr}, // first heartbeat fails
		// fast-retry hits the default = 200 → recovery
	)
	r := newTestRegistrar(t, ft)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop(context.Background())

	// Let recovery happen.
	if fatal := drainFatal(t, r, 400*time.Millisecond); fatal != nil {
		t.Errorf("unexpected fatal during recovery: %v", fatal)
	}
	if cnt := r.consecutiveTransportFailures(); cnt != 0 {
		t.Errorf("after recovery, consecutiveTransportFailures = %d, want 0", cnt)
	}
	if r.pendingFastRetry() {
		t.Errorf("after recovery, fast-retry timer still pending")
	}

	// Now inject one more transport error. Because state was cleared, this
	// must be treated as a fresh first-failure (NOT fatal).
	ft.enqueue(fakeResponse{err: connRefusedErr})
	if fatal := drainFatal(t, r, 200*time.Millisecond); fatal != nil {
		t.Errorf("post-recovery first transport failure should not be fatal: %v", fatal)
	}
}

// Scenario 8: Fatal triggers orderly shutdown — exposed via Fatal() channel.
// The channel must deliver the error so Daemon.Run can act on it.
func TestFatalChannelDeliversError(t *testing.T) {
	ft := newFakeTransport(fakeResponse{status: 401})
	ft.enqueue(fakeResponse{status: 200}) // initial Start
	r := newTestRegistrar(t, ft)
	if err := r.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer r.Stop(context.Background())

	select {
	case err := <-r.Fatal():
		if err == nil {
			t.Fatal("Fatal() delivered nil error")
		}
		if !errors.Is(err, argus.ErrUnauthorized) {
			t.Errorf("Fatal() err = %v, want ErrUnauthorized", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Fatal() did not deliver an error within 1s")
	}
}

// Scenario 9: launchd plist contract — the deployed plist must already have
// KeepAlive.SuccessfulExit=false and ThrottleInterval=60 so a fatal exit
// triggers an automatic, throttled restart. This test guards against the
// plist being edited in a way that breaks the recovery contract.
func TestLaunchdPlistRecoveryContract(t *testing.T) {
	const path = "../../deploy/com.anutron.plannotator-argus.plist"
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read plist: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, "<key>SuccessfulExit</key>") {
		t.Fatal("plist missing SuccessfulExit key")
	}
	// SuccessfulExit must be false (allow whitespace variance between the key
	// and the value).
	idx := strings.Index(body, "<key>SuccessfulExit</key>")
	tail := body[idx:]
	cut := tail
	if len(cut) > 200 {
		cut = cut[:200]
	}
	if !strings.Contains(cut, "<false/>") {
		t.Errorf("plist does not set SuccessfulExit to false; window:\n%s", cut)
	}
	if !strings.Contains(body, "<key>ThrottleInterval</key>") {
		t.Fatal("plist missing ThrottleInterval key")
	}
	idx = strings.Index(body, "<key>ThrottleInterval</key>")
	tail = body[idx:]
	cut = tail
	if len(cut) > 200 {
		cut = cut[:200]
	}
	if !strings.Contains(cut, "<integer>60</integer>") {
		t.Errorf("plist does not set ThrottleInterval to 60; window:\n%s", cut)
	}
}
