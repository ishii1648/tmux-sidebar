package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindCodexTranscriptPathFrom(t *testing.T) {
	dir := t.TempDir()
	sessionID := "019dd846-c1d0-7fd0-ac6d-105cef99fd35"
	nested := filepath.Join(dir, "2026", "04", "29")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(nested, "rollout-2026-04-29T17-06-49-"+sessionID+".jsonl")
	if err := os.WriteFile(want, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "other.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := findCodexTranscriptPathFrom(dir, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestExtractCodexInitialPrompt_ResponseItem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := `{"type":"session_meta","payload":{"id":"sid"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/tmp</cwd>\n</environment_context>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"調査して"}]}}
{"type":"event_msg","payload":{"type":"user_message","message":"調査して"}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ExtractCodexInitialPrompt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "調査して" {
		t.Fatalf("prompt = %q, want %q", got, "調査して")
	}
}

func TestExtractCodexInitialPrompt_EventMsg(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := `{"type":"session_meta","payload":{"id":"sid"}}
{"type":"event_msg","payload":{"type":"user_message","message":"実装して"}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ExtractCodexInitialPrompt(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != "実装して" {
		t.Fatalf("prompt = %q, want %q", got, "実装して")
	}
}

func TestExtractInitialPromptForAgent_Codex(t *testing.T) {
	oldDir := DefaultCodexSessionsDir
	t.Cleanup(func() { DefaultCodexSessionsDir = oldDir })

	dir := t.TempDir()
	DefaultCodexSessionsDir = dir
	sessionID := "019dd846-c1d0-7fd0-ac6d-105cef99fd35"
	path := filepath.Join(dir, "rollout-"+sessionID+".jsonl")
	data := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Codex prompt"}]}}
`
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := ExtractInitialPromptForAgent("codex", sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Codex prompt" {
		t.Fatalf("prompt = %q, want %q", got, "Codex prompt")
	}
}
