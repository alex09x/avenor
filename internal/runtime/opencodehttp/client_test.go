package opencodehttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/runtime"
)

func TestSendMessageAgentModelForwarding(t *testing.T) {
	var gotAgent string
	var gotModel map[string]string
	var gotParts []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			// Keep connection open; test will cancel context.
			<-r.Context().Done()
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		gotAgent, _ = body["agent"].(string)
		if m, ok := body["model"].(map[string]any); ok {
			gotModel = map[string]string{}
			for k, v := range m {
				if s, ok := v.(string); ok {
					gotModel[k] = s
				}
			}
		}
		if parts, ok := body["parts"].([]any); ok {
			gotParts = make([]map[string]any, 0, len(parts))
			for _, p := range parts {
				if m, ok := p.(map[string]any); ok {
					gotParts = append(gotParts, m)
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"info":  map[string]any{"finish": "stop"},
			"parts": []any{},
		})
	}))
	defer srv.Close()

	client := NewClient(ClientOptions{BaseURL: srv.URL})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("health check: %v", err)
	}

	payload := map[string]any{
		"parts": []map[string]any{{"type": "text", "text": "hello"}},
		"agent": "jockey",
		"model": map[string]string{"providerID": "deepseek", "modelID": "deepseek-v4-pro"},
	}
	_, err := client.SendMessage(context.Background(), "ses_test", payload)
	if err != nil {
		t.Fatalf("SendMessage error = %v", err)
	}
	if gotAgent != "jockey" {
		t.Errorf("agent = %q, want %q", gotAgent, "jockey")
	}
	if gotModel["providerID"] != "deepseek" || gotModel["modelID"] != "deepseek-v4-pro" {
		t.Errorf("model = %v, want providerID=deepseek modelID=deepseek-v4-pro", gotModel)
	}
	if len(gotParts) != 1 || gotParts[0]["type"] != "text" || gotParts[0]["text"] != "hello" {
		t.Errorf("parts = %v, want [{type:text text:hello}]", gotParts)
	}
}

func TestSendMessageWithoutAgentModel(t *testing.T) {
	var gotAgent string
	var gotModel any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotAgent, _ = body["agent"].(string)
		gotModel = body["model"]
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"info":  map[string]any{"finish": "stop"},
			"parts": []any{},
		})
	}))
	defer srv.Close()

	client := NewClient(ClientOptions{BaseURL: srv.URL})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("health check: %v", err)
	}

	payload := map[string]any{
		"parts": []map[string]any{{"type": "text", "text": "hello"}},
	}
	_, err := client.SendMessage(context.Background(), "ses_test", payload)
	if err != nil {
		t.Fatalf("SendMessage error = %v", err)
	}
	if gotAgent != "" {
		t.Errorf("agent = %q, want empty", gotAgent)
	}
	if gotModel != nil {
		t.Errorf("model = %v, want nil", gotModel)
	}
}

func TestAbortCorrectPath(t *testing.T) {
	var gotMethod, gotPath string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
			return
		}
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewClient(ClientOptions{BaseURL: srv.URL})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("health check: %v", err)
	}

	if err := client.Abort(context.Background(), "ses_abc123"); err != nil {
		t.Fatalf("Abort error = %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/session/ses_abc123/abort" {
		t.Errorf("path = %q, want /session/ses_abc123/abort", gotPath)
	}
}

func TestCreateSessionReturnsID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"id": "ses_test123"})
	}))
	defer srv.Close()

	client := NewClient(ClientOptions{BaseURL: srv.URL})
	id, err := client.CreateSession(context.Background())
	if err != nil {
		t.Fatalf("CreateSession error = %v", err)
	}
	if id != "ses_test123" {
		t.Errorf("session ID = %q, want %q", id, "ses_test123")
	}
}

func TestBasicAuthFromURL(t *testing.T) {
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			gotAuth = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusOK)
			return
		}
	}))
	defer srv.Close()

	// Test that basic auth is extracted from URL userinfo and sent.
	client := NewClient(ClientOptions{
		BaseURL:  srv.URL,
		Username: "testuser",
		Password: "testpass",
	})
	if err := client.Health(context.Background()); err != nil {
		t.Fatalf("health check: %v", err)
	}
	if !strings.Contains(gotAuth, "Basic ") {
		t.Errorf("Authorization header = %q, want Basic auth", gotAuth)
	}
}

func TestMapModelFromProvider(t *testing.T) {
	// Verify mapModel called from Provider.Prompt payload construction.
	var gotPayload map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/global/health" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/event" {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			<-r.Context().Done()
			return
		}
		if strings.HasSuffix(r.URL.Path, "/message") {
			json.NewDecoder(r.Body).Decode(&gotPayload)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{
			"info":  map[string]any{"finish": "stop"},
			"parts": []any{},
		})
	}))
	defer srv.Close()

	p, _ := NewWithOptions(runtime.StartOptions{
		Agent:     "jockey",
		Model:     "deepseek/deepseek-v4-pro",
		ServerURL: srv.URL,
	})
	// We need to import runtime - it's already imported.
	_ = p.(*Provider)

	// Use concrete type to avoid interface method call.
	prov := p.(*Provider)
	// Manually ensure client.
	c := NewClient(ClientOptions{BaseURL: srv.URL})
	prov.mu.Lock()
	prov.client = c
	prov.events = make(chan events.Event, 1)
	prov.mu.Unlock()

	if err := prov.Prompt(context.Background(), "ses_test", "hello"); err != nil {
		t.Fatalf("Prompt error = %v", err)
	}
	if gotPayload["agent"] != "jockey" {
		t.Errorf("agent = %v, want jockey", gotPayload["agent"])
	}
	model, _ := gotPayload["model"].(map[string]any)
	if model == nil {
		t.Fatal("model is nil")
	}
	if model["providerID"] != "deepseek" {
		t.Errorf("providerID = %v, want deepseek", model["providerID"])
	}
	if model["modelID"] != "deepseek-v4-pro" {
		t.Errorf("modelID = %v, want deepseek-v4-pro", model["modelID"])
	}
}
