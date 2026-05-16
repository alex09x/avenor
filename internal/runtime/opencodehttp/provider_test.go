package opencodehttp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/runtime"
)

func TestNewWithOptions(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{
		Agent: "jockey",
		Model: "deepseek/deepseek-v4-pro",
	})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	if p == nil {
		t.Fatal("NewWithOptions returned nil provider")
	}
}

func TestStartFailsWithoutServerURL(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	_, err = p.Start(context.Background(), runtime.StartOptions{})
	if err == nil {
		t.Fatal("Start without ServerURL should fail")
	}
}

func TestCapabilities(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	caps, err := p.Capabilities(context.Background())
	if err != nil {
		t.Fatalf("Capabilities error = %v", err)
	}
	if caps.Backend != "opencode-http" {
		t.Errorf("Backend = %q, want %q", caps.Backend, "opencode-http")
	}
	if caps.Permissions {
		t.Error("Permissions should be false until HTTP permission behavior is verified")
	}
	if !caps.Resume {
		t.Error("Resume should be true")
	}
	if !caps.ExternalServerURL {
		t.Error("ExternalServerURL should be true")
	}
	if caps.SubprocessDiscovery {
		t.Error("SubprocessDiscovery should be false")
	}
	if !caps.ModelSelection {
		t.Error("ModelSelection should be true")
	}
}

func TestResumeFailsWithoutServerURL(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	_, err = p.Resume(context.Background(), "ses_test")
	if err == nil {
		t.Fatal("Resume without ServerURL should fail")
	}
}

func TestEventsFailsBeforeStart(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	_, err = p.Events(context.Background(), "ses_test")
	if err == nil {
		t.Fatal("Events before Start should fail")
	}
}

func TestAnswerPermissionFailsBeforeStart(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	err = p.AnswerPermission(context.Background(), "ses_test", "req_1", runtime.PermissionResponse{})
	if err == nil {
		t.Fatal("AnswerPermission before Start should fail")
	}
}

func TestOptionMerge(t *testing.T) {
	base := runtime.StartOptions{
		Agent: "default-agent",
		Model: "default-model",
		Dir:   "/default",
	}
	override := runtime.StartOptions{
		Agent:     "jockey",
		ServerURL: "http://localhost:4096",
	}
	merged := mergeStartOptions(base, override)
	if merged.Agent != "jockey" {
		t.Errorf("Agent = %q, want %q", merged.Agent, "jockey")
	}
	if merged.Model != "default-model" {
		t.Errorf("Model = %q, want %q (override is empty, should keep base)", merged.Model, "default-model")
	}
	if merged.ServerURL != "http://localhost:4096" {
		t.Errorf("ServerURL = %q, want %q", merged.ServerURL, "http://localhost:4096")
	}
	if merged.Dir != "/default" {
		t.Errorf("Dir = %q, want %q", merged.Dir, "/default")
	}
}

func TestMapModel(t *testing.T) {
	tests := []struct {
		input    string
		provider string
		model    string
	}{
		{"deepseek/deepseek-v4-pro", "deepseek", "deepseek-v4-pro"},
		{"anthropic/claude-sonnet-4-20250514", "anthropic", "claude-sonnet-4-20250514"},
		{"just-a-model", "", "just-a-model"},
		{"", "", ""},
	}
	for _, tt := range tests {
		result := mapModel(tt.input)
		if result["providerID"] != tt.provider {
			t.Errorf("mapModel(%q).providerID = %q, want %q", tt.input, result["providerID"], tt.provider)
		}
		if result["modelID"] != tt.model {
			t.Errorf("mapModel(%q).modelID = %q, want %q", tt.input, result["modelID"], tt.model)
		}
	}
}

func TestClose(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	// Close should not panic even before Start.
	if closer, ok := p.(interface{ Close() error }); ok {
		if err := closer.Close(); err != nil {
			t.Errorf("Close error = %v", err)
		}
	}
}

func TestPromptFailsBeforeStart(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	err = p.Prompt(context.Background(), "ses_test", "hello")
	if err == nil {
		t.Fatal("Prompt before Start should fail")
	}
}

func TestCancelFailsBeforeStart(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	err = p.Cancel(context.Background(), "ses_test")
	if err == nil {
		t.Fatal("Cancel before Start should fail")
	}
}

func TestResumeWithEmptyID(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{
		ServerURL: "http://localhost:4096",
	})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	_, err = p.Resume(context.Background(), "")
	if err == nil {
		t.Fatal("Resume with empty sessionID should fail")
	}
}

func TestEventsBroadcastToConcurrentSubscribers(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	prov := p.(*Provider)
	prov.mu.Lock()
	prov.started = true
	prov.mu.Unlock()

	ctxA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	chA, err := prov.Events(ctxA, "ses_a")
	if err != nil {
		t.Fatalf("Events(ses_a) error = %v", err)
	}
	ctxB, cancelB := context.WithCancel(context.Background())
	defer cancelB()
	chB, err := prov.Events(ctxB, "ses_b")
	if err != nil {
		t.Fatalf("Events(ses_b) error = %v", err)
	}

	prov.publish(events.Event{Event: "agent.message_chunk", SessionID: "ses_a"})
	prov.publish(events.Event{Event: "agent.message_chunk", SessionID: "ses_b"})

	assertEvent := func(name string, ch <-chan events.Event, wantSessionID string) {
		t.Helper()
		select {
		case evt := <-ch:
			if evt.SessionID != wantSessionID {
				t.Fatalf("%s got session %q, want %q", name, evt.SessionID, wantSessionID)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s timed out waiting for event", name)
		}
	}
	assertEvent("subscriber A", chA, "ses_a")
	assertEvent("subscriber B", chB, "ses_b")
}

func TestPromptUsesStartOptionsForSession(t *testing.T) {
	var gotPayload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			w.WriteHeader(http.StatusOK)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
		case "/session":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ses_test"})
		case "/session/ses_test/message":
			if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{"finish": "stop"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	session, err := p.Start(context.Background(), runtime.StartOptions{
		Agent:     "jockey",
		Model:     "deepseek/deepseek-v4-pro",
		ServerURL: srv.URL,
	})
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer p.(*Provider).Close()

	if err := p.Prompt(context.Background(), session.SessionID, "hello"); err != nil {
		t.Fatalf("Prompt error = %v", err)
	}
	if gotPayload["agent"] != "jockey" {
		t.Fatalf("agent = %v, want jockey", gotPayload["agent"])
	}
	model, _ := gotPayload["model"].(map[string]any)
	if model["providerID"] != "deepseek" || model["modelID"] != "deepseek-v4-pro" {
		t.Fatalf("model = %v, want deepseek/deepseek-v4-pro", model)
	}
}

func TestStartHealthUsesCallerContext(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			<-r.Context().Done()
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err = p.Start(ctx, runtime.StartOptions{ServerURL: srv.URL})
	if err == nil {
		t.Fatal("Start with cancelled context should fail")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Start error = %v, want context.Canceled", err)
	}
}

func TestPromptPublishesSessionEndFromMessageResponse(t *testing.T) {
	var gotPayload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			w.WriteHeader(http.StatusOK)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
		case "/session":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ses_test"})
		case "/session/ses_test/message":
			if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{"finish": "stop"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	session, err := p.Start(context.Background(), runtime.StartOptions{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer p.(*Provider).Close()

	eventCtx, cancelEvents := context.WithCancel(context.Background())
	defer cancelEvents()
	ch, err := p.Events(eventCtx, session.SessionID)
	if err != nil {
		t.Fatalf("Events error = %v", err)
	}
	if err := p.Prompt(context.Background(), session.SessionID, "hello"); err != nil {
		t.Fatalf("Prompt error = %v", err)
	}
	if gotPayload == nil {
		t.Fatal("server did not receive prompt payload")
	}
	select {
	case evt := <-ch:
		if evt.Event != "session.end" || evt.Fields["stop_reason"] != "end_turn" {
			t.Fatalf("event = %+v, want session.end end_turn", evt)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for response-derived session.end")
	}
}

func TestPromptResetsSessionEndGuardForNextTurn(t *testing.T) {
	var messageRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			w.WriteHeader(http.StatusOK)
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
		case "/session":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ses_test"})
		case "/session/ses_test/message":
			messageRequests.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"info": map[string]any{"finish": "stop"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	session, err := p.Start(context.Background(), runtime.StartOptions{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer p.(*Provider).Close()

	eventCtx, cancelEvents := context.WithCancel(context.Background())
	defer cancelEvents()
	ch, err := p.Events(eventCtx, session.SessionID)
	if err != nil {
		t.Fatalf("Events error = %v", err)
	}

	for i := 0; i < 2; i++ {
		if err := p.Prompt(context.Background(), session.SessionID, fmt.Sprintf("hello %d", i)); err != nil {
			t.Fatalf("Prompt %d error = %v", i, err)
		}
		select {
		case evt := <-ch:
			if evt.Event != "session.end" || evt.Fields["stop_reason"] != "end_turn" {
				t.Fatalf("turn %d event = %+v, want session.end end_turn", i, evt)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for turn %d session.end", i)
		}
	}
	if got := messageRequests.Load(); got != 2 {
		t.Fatalf("message requests = %d, want 2", got)
	}
}

func TestPublishDropsSlowSubscriberWithoutBlocking(t *testing.T) {
	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	prov := p.(*Provider)
	prov.mu.Lock()
	prov.started = true
	slow := make(chan events.Event)
	prov.subscribers[slow] = "ses_test"
	prov.mu.Unlock()

	done := make(chan struct{})
	go func() {
		prov.publish(events.Event{Event: "agent.message_chunk", SessionID: "ses_test"})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("publish blocked on slow subscriber")
	}
	prov.mu.Lock()
	_, ok := prov.subscribers[slow]
	prov.mu.Unlock()
	if ok {
		t.Fatal("slow subscriber was not removed")
	}
}

func TestStreamEventsReconnectsAfterDisconnect(t *testing.T) {
	var eventRequests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/global/health":
			w.WriteHeader(http.StatusOK)
		case "/session":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": "ses_test"})
		case "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			n := eventRequests.Add(1)
			if n == 1 {
				fmt.Fprint(w, `data: {"type":"server.heartbeat","properties":{}}`+"\n\n")
				return
			}
			fmt.Fprint(w, `data: {"type":"message.part.delta","properties":{"sessionID":"ses_test","messageID":"msg_1","partID":"prt_1","field":"text","delta":"after reconnect"}}`+"\n\n")
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	p, err := NewWithOptions(runtime.StartOptions{})
	if err != nil {
		t.Fatalf("NewWithOptions error = %v", err)
	}
	session, err := p.Start(context.Background(), runtime.StartOptions{ServerURL: srv.URL})
	if err != nil {
		t.Fatalf("Start error = %v", err)
	}
	defer p.(*Provider).Close()

	eventCtx, cancelEvents := context.WithCancel(context.Background())
	defer cancelEvents()
	ch, err := p.Events(eventCtx, session.SessionID)
	if err != nil {
		t.Fatalf("Events error = %v", err)
	}
	select {
	case evt := <-ch:
		if evt.Event != "agent.message_chunk" || evt.Fields["delta"] != "after reconnect" {
			t.Fatalf("event = %+v, want message chunk after reconnect", evt)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reconnected stream event")
	}
}
