package dispatch

import (
	"strings"
	"testing"
)

func TestBranchFromPrompt(t *testing.T) {
	cases := []struct {
		prompt string
		want   string
	}{
		{"Add health check endpoint", "feat/add-health-check-endpoint"},
		{"  hello  world  ", "feat/hello-world"},
		{"FOO bar", "feat/foo-bar"},
		{"line1\nline2", "feat/line1"},
		{"line1\rline2", "feat/line1"},     // bare CR (terminal paste artefact)
		{"line1\r\nline2", "feat/line1"},   // CRLF
		// 41 alnum chars → truncated to 40
		{strings.Repeat("a", 41), "feat/" + strings.Repeat("a", 40)},
	}
	for _, c := range cases {
		got := BranchFromPrompt(c.prompt)
		if got != c.want {
			t.Errorf("BranchFromPrompt(%q) = %q, want %q", c.prompt, got, c.want)
		}
	}
}

func TestBranchFromPromptFallback(t *testing.T) {
	// Non-ASCII-only input → timestamp slug.
	got := BranchFromPrompt("日本語のみ")
	if !strings.HasPrefix(got, "feat/") {
		t.Fatalf("expected feat/ prefix, got %q", got)
	}
	rest := strings.TrimPrefix(got, "feat/")
	if len(rest) == 0 {
		t.Fatalf("slug should fall back to timestamp, got empty")
	}
}

func TestParseBranchPrefix(t *testing.T) {
	cases := []struct {
		prompt          string
		wantBranch      string
		wantCheckout    bool
		wantRest        string
	}{
		{":feat/foo", "feat/foo", true, ""},
		{":feat/foo\nrest of body", "feat/foo", true, "rest of body"},
		{"  :feat/bar  ", "feat/bar", true, ""},
		{"plain prompt", "", false, "plain prompt"},
		{":", "", false, ":"},
	}
	for _, c := range cases {
		b, ck, rest := ParseBranchPrefix(c.prompt)
		if b != c.wantBranch || ck != c.wantCheckout || rest != c.wantRest {
			t.Errorf("ParseBranchPrefix(%q) = (%q,%v,%q), want (%q,%v,%q)",
				c.prompt, b, ck, rest, c.wantBranch, c.wantCheckout, c.wantRest)
		}
	}
}
