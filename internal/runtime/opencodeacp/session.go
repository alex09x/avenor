package opencodeacp

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/sdougbrown/avenor/internal/events"
)

const ProbePrompt = "List the files in this directory and exit."

type Session struct {
	Client    *Client
	SessionID string
}

func (c *Client) NewSession(ctx context.Context) (*Session, error) {
	result, err := c.request(ctx, "session/new", map[string]any{
		"cwd":        c.dir,
		"mcpServers": []any{},
	})
	if err != nil {
		return nil, err
	}

	var payload struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &payload); err != nil {
		return nil, err
	}
	if payload.SessionID == "" {
		return nil, errors.New("session/new response missing sessionId")
	}

	return &Session{Client: c, SessionID: payload.SessionID}, nil
}

func (c *Client) LoadSession(ctx context.Context, sessionID string) (*Session, error) {
	_, err := c.request(ctx, "session/load", map[string]any{
		"sessionId":  sessionID,
		"cwd":        c.dir,
		"mcpServers": []any{},
	})
	if err != nil {
		return nil, err
	}
	return &Session{Client: c, SessionID: sessionID}, nil
}

func (c *Client) ResumeSession(ctx context.Context, sessionID string) (*Session, error) {
	_, err := c.request(ctx, "session/resume", map[string]any{
		"sessionId":  sessionID,
		"cwd":        c.dir,
		"mcpServers": []any{},
	})
	if err != nil {
		return nil, err
	}
	return &Session{Client: c, SessionID: sessionID}, nil
}

func (s *Session) Prompt(ctx context.Context, prompt string) (events.Event, error) {
	result, err := s.Client.request(ctx, "session/prompt", map[string]any{
		"sessionId": s.SessionID,
		"prompt": []map[string]any{
			{
				"type": "text",
				"text": prompt,
			},
		},
	})
	if err != nil {
		return events.Event{}, err
	}
	return mapPromptResponse(s.SessionID, result)
}

func (s *Session) Cancel() error {
	return s.Client.notify("session/cancel", map[string]any{
		"sessionId": s.SessionID,
	})
}

func (s *Session) Close(ctx context.Context) error {
	_, err := s.Client.request(ctx, "session/close", map[string]any{
		"sessionId": s.SessionID,
	})
	return err
}

func RunProbe(ctx context.Context, dir string) ([]events.Event, error) {
	return RunProbeWithPrompt(ctx, dir, ProbePrompt)
}

func RunProbeWithPrompt(ctx context.Context, dir, prompt string) ([]events.Event, error) {
	client, err := NewClient(ctx, dir)
	if err != nil {
		return nil, err
	}
	defer client.Close()

	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := client.Initialize(initCtx); err != nil {
		return nil, err
	}

	session, err := client.NewSession(initCtx)
	if err != nil {
		return nil, err
	}

	eventsCh := client.Events()
	promptDone := make(chan struct {
		event events.Event
		err   error
	}, 1)
	go func() {
		event, err := session.Prompt(ctx, prompt)
		promptDone <- struct {
			event events.Event
			err   error
		}{event: event, err: err}
	}()

	var transcript []events.Event
	for {
		select {
		case event, ok := <-eventsCh:
			if ok {
				transcript = append(transcript, event)
			}
		case result := <-promptDone:
			if result.err != nil {
				return transcript, result.err
			}
			transcript = append(transcript, result.event)
			return transcript, nil
		case <-ctx.Done():
			_ = session.Cancel()
			return transcript, ctx.Err()
		}
	}
}
