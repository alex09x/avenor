package permission

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
	"github.com/sdougbrown/avenor/internal/runtime"
)

const (
	DefaultTimeout      = 10 * time.Minute
	DefaultPollInterval = 500 * time.Millisecond
)

// FileRequest is the JSON written to <base>.req.
//
// Example:
//
//	{
//	  "request_id": "17",
//	  "session_id": "ses_...",
//	  "tool": "bash",
//	  "question": "Run command?",
//	  "options": [{"optionId":"allow","kind":"allow"}],
//	  "payload": {"request_id":"17","tool":"bash","options":[...]}
//	}
//
// The operator or answer-jockey skill replies by writing <base>.req.response:
//
//	{"outcome":"selected","option_id":"allow","message":"optional note"}
type FileRequest struct {
	RequestID string         `json:"request_id"`
	SessionID string         `json:"session_id,omitempty"`
	Tool      string         `json:"tool,omitempty"`
	Question  string         `json:"question,omitempty"`
	Options   any            `json:"options,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

type FileHandler struct {
	BasePath     string
	Timeout      time.Duration
	PollInterval time.Duration
}

func NewFileHandler(basePath string) *FileHandler {
	return &FileHandler{
		BasePath:     basePath,
		Timeout:      DefaultTimeout,
		PollInterval: DefaultPollInterval,
	}
}

func (h *FileHandler) Handle(ctx context.Context, provider runtime.Provider, event events.Event, emit func(events.Event) error) error {
	if h == nil {
		return errors.New("permission file handler is nil")
	}
	if h.BasePath == "" {
		return errors.New("permission file handler path is required")
	}
	if provider == nil {
		return errors.New("permission provider is required")
	}

	request := requestFromEvent(event)
	if request.RequestID == "" {
		return errors.New("permission.request missing request_id")
	}

	requestPath := h.BasePath + ".req"
	responsePath := requestPath + ".response"
	_ = os.Remove(responsePath)

	data, err := json.MarshalIndent(request, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.WriteFile(requestPath, data, 0o600); err != nil {
		return err
	}

	if emit != nil {
		if err := emit(normalizedPermissionEvent(request)); err != nil {
			return err
		}
	}

	response, err := h.waitForResponse(ctx, responsePath)
	if err != nil {
		return err
	}
	return provider.AnswerPermission(ctx, event.SessionID, request.RequestID, response)
}

func (h *FileHandler) waitForResponse(ctx context.Context, path string) (runtime.PermissionResponse, error) {
	timeout := h.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	interval := h.PollInterval
	if interval == 0 {
		interval = DefaultPollInterval
	}

	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		response, err := readResponse(path)
		if err == nil {
			return response, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return runtime.PermissionResponse{}, err
		}

		select {
		case <-waitCtx.Done():
			return runtime.PermissionResponse{}, fmt.Errorf("wait for permission response: %w", waitCtx.Err())
		case <-ticker.C:
		}
	}
}

func readResponse(path string) (runtime.PermissionResponse, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return runtime.PermissionResponse{}, err
	}
	var response runtime.PermissionResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return runtime.PermissionResponse{}, err
	}
	if response.Outcome == "" {
		response.Outcome = "selected"
	}
	return response, nil
}

func requestFromEvent(event events.Event) FileRequest {
	fields := map[string]any{}
	for k, v := range event.Fields {
		fields[k] = v
	}
	requestID, _ := fields["request_id"].(string)
	tool, _ := fields["tool"].(string)
	question, _ := fields["question"].(string)
	if question == "" {
		question, _ = fields["message"].(string)
	}

	return FileRequest{
		RequestID: requestID,
		SessionID: event.SessionID,
		Tool:      tool,
		Question:  question,
		Options:   fields["options"],
		Payload:   fields,
	}
}

func normalizedPermissionEvent(request FileRequest) events.Event {
	fields := map[string]any{
		"request_id": request.RequestID,
	}
	if request.Tool != "" {
		fields["tool"] = request.Tool
	}
	if request.Question != "" {
		fields["question"] = request.Question
	}
	if request.Options != nil {
		fields["options"] = request.Options
	}
	return events.Event{
		Event:     "permission.request",
		SessionID: request.SessionID,
		Fields:    fields,
	}
}
