package digest

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func TestDigestLine(t *testing.T) {
	long := strings.Repeat("x", 121)
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "agent message content text",
			raw:  `{"event":"agent.message_chunk","session_id":"ses_1","content":{"text":"hello\n\tworld","type":"text"}}`,
			want: "EVENT agent.message_chunk ses_1 hello world",
		},
		{
			name: "agent thought content text",
			raw:  `{"event":"agent.thought_chunk","session_id":"ses_1","content":{"text":"thinking","type":"text"}}`,
			want: "EVENT agent.thought_chunk ses_1 thinking",
		},
		{
			name: "user array content is empty",
			raw:  `{"event":"user.message_chunk","session_id":"ses_1","content":[{"text":"ignore me"}]}`,
			want: "EVENT user.message_chunk ses_1 ",
		},
		{
			name: "tool kind title status",
			raw:  `{"event":"tool.call","session_id":"ses_1","kind":"shell","title":"go test","status":"running"}`,
			want: "EVENT tool.call ses_1 shell:go test [running]",
		},
		{
			name: "tool title only",
			raw:  `{"event":"tool.call_update","session_id":"ses_1","title":"build"}`,
			want: "EVENT tool.call_update ses_1 build",
		},
		{
			name: "permission falls back to tool",
			raw:  `{"event":"permission.request","session_id":"ses_1","question":"","tool":"exec"}`,
			want: "EVENT permission.request ses_1 exec",
		},
		{
			name: "plan falls back to title",
			raw:  `{"event":"session.plan","session_id":"ses_1","label":"","title":"Draft plan"}`,
			want: "EVENT session.plan ses_1 Draft plan",
		},
		{
			name: "session end empty stop reason",
			raw:  `{"event":"session.end","session_id":"ses_1"}`,
			want: "EVENT session.end ses_1 stop_reason=",
		},
		{
			name: "unknown event",
			raw:  `{"event":"session.started","session_id":"ses_1","message":"ignored"}`,
			want: "EVENT session.started ses_1 ",
		},
		{
			name: "sessionID fallback and truncation",
			raw:  `{"event":"agent.message_chunk","sessionID":"ses_2","content":{"text":"` + long + `","type":"text"}}`,
			want: "EVENT agent.message_chunk ses_2 " + strings.Repeat("x", 120),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := DigestLine([]byte(tt.raw))
			if err != nil {
				t.Fatalf("DigestLine() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("DigestLine() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDigestLineMalformed(t *testing.T) {
	if _, err := DigestLine([]byte(`{"event":`)); err == nil {
		t.Fatal("DigestLine() error = nil, want malformed JSON error")
	}
}

func TestStreamGolden(t *testing.T) {
	fixtures := []string{
		"events-vocabulary",
		"happy-path",
		"permission-request",
	}

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			inputPath := filepath.Join("..", "..", "testdata", "digest", fixture+".ndjson")
			goldenPath := filepath.Join("..", "..", "testdata", "digest", fixture+".golden")

			input, err := os.Open(inputPath)
			if err != nil {
				t.Fatalf("open input: %v", err)
			}
			defer input.Close()

			var out bytes.Buffer
			if err := Stream(input, &out, Options{}); err != nil {
				t.Fatalf("Stream() error = %v", err)
			}

			if *update {
				if err := os.WriteFile(goldenPath, out.Bytes(), 0o644); err != nil {
					t.Fatalf("update golden: %v", err)
				}
			}

			want, err := os.ReadFile(goldenPath)
			if err != nil {
				t.Fatalf("read golden: %v", err)
			}
			if got := out.String(); got != string(want) {
				t.Fatalf("Stream() mismatch\n--- got ---\n%s--- want ---\n%s", got, want)
			}
		})
	}
}

func TestStreamJSONFormat(t *testing.T) {
	input := "{\"event\":\"session.plan\"}\n\nnot-json\n{\"event\":\"session.end\"}\n"
	var out bytes.Buffer
	if err := Stream(strings.NewReader(input), &out, Options{Format: "json"}); err != nil {
		t.Fatalf("Stream() error = %v", err)
	}

	want := "{\"event\":\"session.plan\"}\n{\"event\":\"session.end\"}\n"
	if got := out.String(); got != want {
		t.Fatalf("Stream() = %q, want %q", got, want)
	}
}
