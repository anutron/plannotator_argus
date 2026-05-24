package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestSessionStoreCreateAndGet(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("annotate")
	if sess.ID == "" {
		t.Error("session ID empty")
	}
	if sess.Status != StatusPending {
		t.Errorf("status = %q, want pending", sess.Status)
	}

	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verb != "annotate" {
		t.Errorf("verb = %q", got.Verb)
	}
}

func TestSessionStoreUnknownSession(t *testing.T) {
	s := NewSessionStore()
	_, err := s.Get("nope")
	if !errors.Is(err, ErrUnknownSession) {
		t.Errorf("err = %v, want ErrUnknownSession", err)
	}
}

func TestSessionStoreMarkComplete(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("annotate")
	s.MarkComplete(sess.ID, json.RawMessage(`{"annotations":[]}`))

	got, err := s.Get(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusComplete {
		t.Errorf("status = %q, want complete", got.Status)
	}
	if string(got.Result) != `{"annotations":[]}` {
		t.Errorf("result = %q", got.Result)
	}
	if got.CompletedAt.IsZero() {
		t.Error("CompletedAt not set")
	}
}

func TestSessionStoreMarkCompleteIdempotent(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("annotate")
	s.MarkComplete(sess.ID, json.RawMessage(`"first"`))
	s.MarkComplete(sess.ID, json.RawMessage(`"second"`)) // no-op
	got, _ := s.Get(sess.ID)
	if string(got.Result) != `"first"` {
		t.Errorf("result mutated by second MarkComplete: %q", got.Result)
	}
}

func TestSessionStoreMarkFailed(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("annotate")
	s.MarkFailed(sess.ID, "stderr summary")
	got, _ := s.Get(sess.ID)
	if got.Status != StatusFailed {
		t.Errorf("status = %q, want failed", got.Status)
	}
	if got.Error != "stderr summary" {
		t.Errorf("error = %q", got.Error)
	}
}

func TestWaitForResolutionPending(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("annotate")
	start := time.Now()
	got, err := s.WaitForResolution(context.Background(), sess.ID, 50*time.Millisecond)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusPending {
		t.Errorf("status = %q, want pending", got.Status)
	}
	if elapsed < 40*time.Millisecond {
		t.Errorf("returned too fast: %v", elapsed)
	}
}

func TestWaitForResolutionUnblocksOnComplete(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("annotate")
	go func() {
		time.Sleep(30 * time.Millisecond)
		s.MarkComplete(sess.ID, json.RawMessage(`"done"`))
	}()
	got, err := s.WaitForResolution(context.Background(), sess.ID, 1*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusComplete {
		t.Errorf("status = %q", got.Status)
	}
}

func TestWaitForResolutionUnknownSession(t *testing.T) {
	s := NewSessionStore()
	_, err := s.WaitForResolution(context.Background(), "nope", 0)
	if !errors.Is(err, ErrUnknownSession) {
		t.Errorf("err = %v, want ErrUnknownSession", err)
	}
}

func TestWaitForResolutionRespectsCtxCancel(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("annotate")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err := s.WaitForResolution(ctx, sess.ID, 1*time.Second)
	if err == nil {
		t.Error("expected ctx error")
	}
}

func TestSetURL(t *testing.T) {
	s := NewSessionStore()
	sess := s.Create("annotate")
	s.SetURL(sess.ID, "http://localhost:9000")
	got, _ := s.Get(sess.ID)
	if got.URL != "http://localhost:9000" {
		t.Errorf("URL = %q", got.URL)
	}
}

func TestGCSkipsPendingDropsCompleted(t *testing.T) {
	s := NewSessionStore()
	pending := s.Create("annotate")
	completed := s.Create("review")
	s.MarkComplete(completed.ID, json.RawMessage(`null`))

	// Rewind completedAt so it is older than the TTL.
	s.mu.Lock()
	s.sessions[completed.ID].CompletedAt = time.Now().Add(-2 * time.Hour)
	s.mu.Unlock()

	removed := s.GC(1 * time.Hour)
	if removed != 1 {
		t.Errorf("GC removed = %d, want 1", removed)
	}
	if _, err := s.Get(pending.ID); err != nil {
		t.Error("pending session was GC'd")
	}
	if _, err := s.Get(completed.ID); !errors.Is(err, ErrUnknownSession) {
		t.Error("completed session was NOT GC'd")
	}
}

func TestSessionStoreConcurrentSafe(t *testing.T) {
	s := NewSessionStore()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess := s.Create("annotate")
			s.SetURL(sess.ID, "http://x")
			s.MarkComplete(sess.ID, json.RawMessage(`null`))
			_, _ = s.Get(sess.ID)
		}()
	}
	wg.Wait()
	if s.Len() != 10 {
		t.Errorf("Len = %d, want 10", s.Len())
	}
}
