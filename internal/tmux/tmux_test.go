package tmux

import (
	"testing"
)

// ── ParseSessions ────────────────────────────────────────────────────────────

func TestParseSessions(t *testing.T) {
	d := tmuxDelim
	tests := []struct {
		name  string
		input string
		want  []Session
	}{
		{
			name:  "single session",
			input: "$1" + d + "main",
			want:  []Session{{ID: "$1", Name: "main"}},
		},
		{
			name:  "multiple sessions",
			input: "$1" + d + "main\n$2" + d + "work",
			want: []Session{
				{ID: "$1", Name: "main"},
				{ID: "$2", Name: "work"},
			},
		},
		{
			name:  "session name with colon",
			input: "$3" + d + "my:session:name",
			want:  []Session{{ID: "$3", Name: "my:session:name"}},
		},
		{
			name:  "session name with spaces",
			input: "$4" + d + "my session",
			want:  []Session{{ID: "$4", Name: "my session"}},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "empty lines are skipped",
			input: "$1" + d + "main\n\n$2" + d + "work",
			want: []Session{
				{ID: "$1", Name: "main"},
				{ID: "$2", Name: "work"},
			},
		},
		{
			name:  "line without delimiter is skipped",
			input: "badline\n$1" + d + "good",
			want:  []Session{{ID: "$1", Name: "good"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSessions(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len=%d, want %d; got=%v want=%v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ── ParseWindows ─────────────────────────────────────────────────────────────

func TestParseWindows(t *testing.T) {
	d := tmuxDelim
	tests := []struct {
		name  string
		input string
		want  []Window
	}{
		{
			name:  "single window",
			input: "$1" + d + "main" + d + "@1" + d + "0" + d + "editor",
			want: []Window{
				{SessionID: "$1", SessionName: "main", ID: "@1", Index: 0, Name: "editor"},
			},
		},
		{
			name: "multiple windows",
			input: "$1" + d + "main" + d + "@1" + d + "0" + d + "editor\n" +
				"$1" + d + "main" + d + "@2" + d + "1" + d + "shell",
			want: []Window{
				{SessionID: "$1", SessionName: "main", ID: "@1", Index: 0, Name: "editor"},
				{SessionID: "$1", SessionName: "main", ID: "@2", Index: 1, Name: "shell"},
			},
		},
		{
			name:  "session name with colon",
			input: "$1" + d + "a:b:c" + d + "@1" + d + "0" + d + "win",
			want: []Window{
				{SessionID: "$1", SessionName: "a:b:c", ID: "@1", Index: 0, Name: "win"},
			},
		},
		{
			name:  "session name with spaces",
			input: "$1" + d + "my session" + d + "@1" + d + "0" + d + "win",
			want: []Window{
				{SessionID: "$1", SessionName: "my session", ID: "@1", Index: 0, Name: "win"},
			},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "invalid window index is skipped",
			input: "$1" + d + "main" + d + "@1" + d + "NaN" + d + "win",
			want:  nil,
		},
		{
			name:  "wrong field count is skipped",
			input: "$1" + d + "main" + d + "@1",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseWindows(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len=%d, want %d; got=%v want=%v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// ── ParseAllPanes ────────────────────────────────────────────────────────────

func TestParseAllPanes_SessionCreated(t *testing.T) {
	d := tmuxDelim
	// 10-field row: session_id|session_name|window_id|window_index|window_name|pane_id|pane_index|window_active|session_attached|session_created
	input := "$1" + d + "main" + d + "@1" + d + "0" + d + "edit" + d + "%1" + d + "0" + d + "1" + d + "1" + d + "1700000000"
	got := parseAllPanes(input)
	if len(got) != 1 {
		t.Fatalf("len = %d want 1", len(got))
	}
	if got[0].SessionCreated.Unix() != 1700000000 {
		t.Errorf("SessionCreated.Unix() = %d want 1700000000", got[0].SessionCreated.Unix())
	}
	if !got[0].WindowActive || !got[0].SessionAttached {
		t.Errorf("flags lost: WindowActive=%v SessionAttached=%v", got[0].WindowActive, got[0].SessionAttached)
	}
}

func TestParseAllPanes_MissingCreatedFalsifiesZero(t *testing.T) {
	d := tmuxDelim
	// session_created field present but unparseable falls through to zero value.
	input := "$1" + d + "main" + d + "@1" + d + "0" + d + "edit" + d + "%1" + d + "0" + d + "0" + d + "0" + d + "garbage"
	got := parseAllPanes(input)
	if len(got) != 1 {
		t.Fatalf("len = %d want 1", len(got))
	}
	if !got[0].SessionCreated.IsZero() {
		t.Errorf("SessionCreated should be zero on parse error, got %v", got[0].SessionCreated)
	}
}

func TestParseAllPanes_RejectsWrongFieldCount(t *testing.T) {
	d := tmuxDelim
	// 9 fields (old format) — rejected silently to surface the schema mismatch via an empty result.
	input := "$1" + d + "main" + d + "@1" + d + "0" + d + "edit" + d + "%1" + d + "0" + d + "1" + d + "1"
	got := parseAllPanes(input)
	if len(got) != 0 {
		t.Errorf("len = %d want 0 (9-field row should be skipped)", len(got))
	}
}

// ── ParsePanes ───────────────────────────────────────────────────────────────

func TestParsePanes(t *testing.T) {
	d := tmuxDelim
	tests := []struct {
		name  string
		input string
		want  []Pane
	}{
		{
			name:  "pane_id %101",
			input: "$1" + d + "@1" + d + "%101" + d + "0",
			want:  []Pane{{SessionID: "$1", WindowID: "@1", ID: "%101", Index: 0, Number: 101}},
		},
		{
			name:  "pane_id %0",
			input: "$1" + d + "@1" + d + "%0" + d + "1",
			want:  []Pane{{SessionID: "$1", WindowID: "@1", ID: "%0", Index: 1, Number: 0}},
		},
		{
			name: "multiple panes",
			input: "$1" + d + "@1" + d + "%1" + d + "0\n" +
				"$1" + d + "@1" + d + "%2" + d + "1\n" +
				"$1" + d + "@2" + d + "%3" + d + "0",
			want: []Pane{
				{SessionID: "$1", WindowID: "@1", ID: "%1", Index: 0, Number: 1},
				{SessionID: "$1", WindowID: "@1", ID: "%2", Index: 1, Number: 2},
				{SessionID: "$1", WindowID: "@2", ID: "%3", Index: 0, Number: 3},
			},
		},
		{
			name:  "empty input",
			input: "",
			want:  nil,
		},
		{
			name:  "pane_id without % prefix — number is 0",
			input: "$1" + d + "@1" + d + "badid" + d + "0",
			want:  []Pane{{SessionID: "$1", WindowID: "@1", ID: "badid", Index: 0, Number: 0}},
		},
		{
			name:  "invalid pane index is skipped",
			input: "$1" + d + "@1" + d + "%1" + d + "NaN",
			want:  nil,
		},
		{
			name:  "wrong field count is skipped",
			input: "$1" + d + "@1" + d + "%1",
			want:  nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parsePanes(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len=%d, want %d; got=%v want=%v", len(got), len(tt.want), got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] got %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
