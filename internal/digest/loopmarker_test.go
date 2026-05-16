package digest

import "testing"

func TestExtractLoopMarker(t *testing.T) {
	tests := []struct {
		name      string
		text      string
		wantDir   string
		wantLabel string
		wantOK    bool
	}{
		{
			name:    "exit directive only",
			text:    "[loop: exit]",
			wantDir: "exit",
			wantOK:  true,
		},
		{
			name:      "exit with label",
			text:      "[loop: exit | tests green]",
			wantDir:   "exit",
			wantLabel: "tests green",
			wantOK:    true,
		},
		{
			name:    "continue directive only",
			text:    "[loop: continue]",
			wantDir: "continue",
			wantOK:  true,
		},
		{
			name:    "abort directive only",
			text:    "[loop: abort]",
			wantDir: "abort",
			wantOK:  true,
		},
		{
			name:      "abort with long label",
			text:      "[loop: abort | architectural issue: layering violation in pkg/db]",
			wantDir:   "abort",
			wantLabel: "architectural issue: layering violation in pkg/db",
			wantOK:    true,
		},
		{
			name:      "abort wins over exit",
			text:      "[loop: exit | minor]\n[loop: abort | critical]",
			wantDir:   "abort",
			wantLabel: "critical",
			wantOK:    true,
		},
		{
			name:    "exit wins over continue",
			text:    "[loop: continue]\n[loop: exit]",
			wantDir: "exit",
			wantOK:  true,
		},
		{
			name:    "abort wins over continue",
			text:    "[loop: continue]\n[loop: abort]",
			wantDir: "abort",
			wantOK:  true,
		},
		{
			name:   "unknown directive ignored",
			text:   "[loop: foobar]",
			wantOK: false,
		},
		{
			name:   "malformed no brackets",
			text:   "just some text",
			wantOK: false,
		},
		{
			name:    "case insensitive LOOP EXIT",
			text:    "[LOOP: EXIT]",
			wantDir: "exit",
			wantOK:  true,
		},
		{
			name:      "label trimming",
			text:      "[loop: exit |   padded  ]",
			wantDir:   "exit",
			wantLabel: "padded",
			wantOK:    true,
		},
		{
			name:      "same severity first wins",
			text:      "[loop: exit | first]\n[loop: exit | second]",
			wantDir:   "exit",
			wantLabel: "first",
			wantOK:    true,
		},
		{
			name:   "inline marker ignored",
			text:   "quoted output says [loop: exit]",
			wantOK: false,
		},
		{
			name:   "code fence marker ignored",
			text:   "```\n[loop: exit]\n```",
			wantOK: false,
		},
		{
			name:   "no marker",
			text:   "this text has no loop marker at all",
			wantOK: false,
		},
		{
			name:   "empty string",
			text:   "",
			wantOK: false,
		},
		{
			name:   "malformed unclosed bracket",
			text:   "[loop: exit | no close",
			wantOK: false,
		},
		{
			name:   "malformed missing colon",
			text:   "[loop exit]",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, label, ok := ExtractLoopMarker(tt.text)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (dir=%q label=%q)", ok, tt.wantOK, dir, label)
			}
			if !ok {
				return
			}
			if dir != tt.wantDir {
				t.Errorf("directive = %q, want %q", dir, tt.wantDir)
			}
			if label != tt.wantLabel {
				t.Errorf("label = %q, want %q", label, tt.wantLabel)
			}
		})
	}
}
