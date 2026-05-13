package permission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/runtime"
)

type fakeProvider struct {
	response  runtime.PermissionResponse
	requestID string
	sessionID string
}

func (f *fakeProvider) Start(ctx context.Context, opts runtime.StartOptions) (runtime.Session, error) {
	return runtime.Session{}, nil
}

func (f *fakeProvider) Resume(ctx context.Context, sessionID string) (runtime.Session, error) {
	return runtime.Session{}, nil
}

func (f *fakeProvider) Prompt(ctx context.Context, sessionID string, prompt string) error {
	return nil
}

func (f *fakeProvider) Cancel(ctx context.Context, sessionID string) error {
	return nil
}

func (f *fakeProvider) Events(ctx context.Context, sessionID string) (<-chan events.Event, error) {
	return nil, nil
}

func (f *fakeProvider) AnswerPermission(ctx context.Context, sessionID string, requestID string, response runtime.PermissionResponse) error {
	f.sessionID = sessionID
	f.requestID = requestID
	f.response = response
	return nil
}

func (f *fakeProvider) Capabilities(ctx context.Context) (runtime.Capabilities, error) {
	return runtime.Capabilities{}, nil
}

func TestFileHandlerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "permission")
	handler := NewFileHandler(base)
	handler.Timeout = 2 * time.Second
	handler.PollInterval = 10 * time.Millisecond

	provider := &fakeProvider{}
	event := events.Event{
		Event:     "permission.request",
		SessionID: "ses_1",
		Fields: map[string]any{
			"request_id": "42",
			"tool":       "bash",
			"question":   "Run command?",
			"options": []any{
				map[string]any{"optionId": "allow", "kind": "allow"},
			},
		},
	}

	emitted := make(chan events.Event, 1)
	done := make(chan error, 1)
	resolved := make(chan Resolution, 1)
	go func() {
		res, err := handler.Handle(context.Background(), provider, event, func(event events.Event) error {
			emitted <- event
			return nil
		})
		resolved <- res
		done <- err
	}()

	reqPath := base + ".req"
	for i := 0; i < 100; i++ {
		if _, err := os.Stat(reqPath); err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	data, err := os.ReadFile(reqPath)
	if err != nil {
		t.Fatalf("read request file: %v", err)
	}
	var request FileRequest
	if err := json.Unmarshal(data, &request); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	if request.RequestID != "42" || request.SessionID != "ses_1" || request.Tool != "bash" || request.Question != "Run command?" {
		t.Fatalf("request = %+v", request)
	}

	response := []byte(`{"outcome":"selected","option_id":"allow"}`)
	if err := os.WriteFile(reqPath+".response", response, 0o600); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Handle returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for handler")
	}

	if provider.sessionID != "ses_1" || provider.requestID != "42" {
		t.Fatalf("answered session=%q request=%q", provider.sessionID, provider.requestID)
	}
	if provider.response.Outcome != "selected" || provider.response.OptionID != "allow" {
		t.Fatalf("response = %+v", provider.response)
	}
	select {
	case res := <-resolved:
		if res.RequestID != "42" || res.OptionID != "allow" {
			t.Fatalf("resolution = %+v", res)
		}
	default:
		t.Fatal("missing resolution")
	}

	select {
	case event := <-emitted:
		if event.Event != "permission.request" || event.Fields["request_id"] != "42" {
			t.Fatalf("emitted = %+v", event)
		}
	default:
		t.Fatal("permission.request was not emitted")
	}
}
