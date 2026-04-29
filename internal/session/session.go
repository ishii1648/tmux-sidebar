// Package session looks up agent session transcripts and extracts the initial prompt.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultIndexPath is the default location of the Claude Code session index.
var DefaultIndexPath = filepath.Join(os.Getenv("HOME"), ".claude", "session-index.jsonl")

// DefaultCodexSessionsDir is the default location of Codex session transcripts.
var DefaultCodexSessionsDir = filepath.Join(os.Getenv("HOME"), ".codex", "sessions")

// indexEntry represents a single line in session-index.jsonl.
type indexEntry struct {
	SessionID  string `json:"session_id"`
	Transcript string `json:"transcript"`
}

// transcriptLine represents a single line in a transcript JSONL file.
type transcriptLine struct {
	Type    string          `json:"type"`
	IsMeta  bool            `json:"isMeta"`
	Message json.RawMessage `json:"message"`
}

// userMessage extracts content from a transcript message field.
type userMessage struct {
	Content string `json:"content"`
}

type codexLine struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type codexResponseItemPayload struct {
	Type    string         `json:"type"`
	Role    string         `json:"role"`
	Content []codexContent `json:"content"`
}

type codexContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type codexEventMsgPayload struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// FindTranscriptPath searches session-index.jsonl for the given session ID
// and returns the transcript file path. Returns empty string if not found.
func FindTranscriptPath(sessionID string) (string, error) {
	return findTranscriptPathFrom(DefaultIndexPath, sessionID)
}

// FindCodexTranscriptPath searches ~/.codex/sessions for the given Codex session ID.
func FindCodexTranscriptPath(sessionID string) (string, error) {
	return findCodexTranscriptPathFrom(DefaultCodexSessionsDir, sessionID)
}

func findTranscriptPathFrom(indexPath, sessionID string) (string, error) {
	f, err := os.Open(indexPath)
	if err != nil {
		return "", fmt.Errorf("open session index: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	// Allow up to 1MB per line for large index entries.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var entry indexEntry
		if json.Unmarshal(line, &entry) != nil {
			continue
		}
		if entry.SessionID == sessionID {
			return entry.Transcript, nil
		}
	}
	return "", nil
}

func findCodexTranscriptPathFrom(sessionsDir, sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}
	var matches []string
	err := filepath.WalkDir(sessionsDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if strings.HasSuffix(name, ".jsonl") && strings.Contains(name, sessionID) {
			matches = append(matches, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk codex sessions: %w", err)
	}
	sort.Strings(matches)
	if len(matches) == 0 {
		return "", nil
	}
	return matches[len(matches)-1], nil
}

// syntheticPromptPrefixes are tag prefixes that indicate a transcript "user"
// message is synthetic (slash command invocation, local command output,
// caveat banner, etc.) rather than a real user prompt.
var syntheticPromptPrefixes = []string{
	"<local-command-caveat>",
	"<local-command-stdout>",
	"<local-command-stderr>",
	"<command-name>",
	"<command-message>",
	"<command-args>",
}

func isSyntheticPrompt(s string) bool {
	for _, p := range syntheticPromptPrefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

func isSyntheticCodexPrompt(s string) bool {
	if isSyntheticPrompt(s) {
		return true
	}
	return strings.HasPrefix(s, "<environment_context>")
}

// ExtractInitialPromptForAgent resolves and extracts the initial prompt for
// the given agent kind. Unknown kinds use the Claude transcript format for
// backward compatibility with legacy state files.
func ExtractInitialPromptForAgent(agent, sessionID string) (string, error) {
	if sessionID == "" {
		return "", nil
	}
	if agent == "codex" {
		transcriptPath, err := FindCodexTranscriptPath(sessionID)
		if err != nil || transcriptPath == "" {
			return "", err
		}
		return ExtractCodexInitialPrompt(transcriptPath)
	}
	transcriptPath, err := FindTranscriptPath(sessionID)
	if err != nil || transcriptPath == "" {
		return "", err
	}
	return ExtractInitialPrompt(transcriptPath)
}

// ExtractInitialPrompt reads a transcript JSONL file and returns the first
// user message content (the initial prompt). Returns empty string if no user
// message is found.
func ExtractInitialPrompt(transcriptPath string) (string, error) {
	// The transcript path from session-index.jsonl may point to either:
	// 1. A flat file: {session_id}.jsonl (older format)
	// 2. A directory-based path that no longer exists as a flat file
	// Try the given path first, then try the directory-based convention.
	f, err := os.Open(transcriptPath)
	if err != nil {
		return "", fmt.Errorf("open transcript: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var tl transcriptLine
		if json.Unmarshal(line, &tl) != nil {
			continue
		}
		if tl.Type != "user" || tl.IsMeta {
			continue
		}
		// Extract content from the message field.
		var msg userMessage
		if json.Unmarshal(tl.Message, &msg) != nil {
			continue
		}
		prompt := strings.TrimSpace(msg.Content)
		if prompt == "" || isSyntheticPrompt(prompt) {
			continue
		}
		return prompt, nil
	}
	return "", nil
}

// ExtractCodexInitialPrompt reads a Codex transcript JSONL file and returns
// the first real user prompt.
func ExtractCodexInitialPrompt(transcriptPath string) (string, error) {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return "", fmt.Errorf("open codex transcript: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		var cl codexLine
		if json.Unmarshal(line, &cl) != nil {
			continue
		}
		prompt := ""
		switch cl.Type {
		case "response_item":
			prompt = extractCodexResponseItemPrompt(cl.Payload)
		case "event_msg":
			prompt = extractCodexEventPrompt(cl.Payload)
		}
		prompt = strings.TrimSpace(prompt)
		if prompt == "" || isSyntheticCodexPrompt(prompt) {
			continue
		}
		return prompt, nil
	}
	return "", nil
}

func extractCodexResponseItemPrompt(payload json.RawMessage) string {
	var p codexResponseItemPayload
	if json.Unmarshal(payload, &p) != nil {
		return ""
	}
	if p.Type != "message" || p.Role != "user" {
		return ""
	}
	var parts []string
	for _, c := range p.Content {
		if c.Type == "input_text" && strings.TrimSpace(c.Text) != "" {
			parts = append(parts, strings.TrimSpace(c.Text))
		}
	}
	return strings.Join(parts, "\n")
}

func extractCodexEventPrompt(payload json.RawMessage) string {
	var p codexEventMsgPayload
	if json.Unmarshal(payload, &p) != nil {
		return ""
	}
	if p.Type != "user_message" {
		return ""
	}
	return p.Message
}
