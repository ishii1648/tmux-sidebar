// Package session looks up Claude Code session transcripts and extracts the initial prompt.
package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// DefaultIndexPath is the default location of the Claude Code session index.
var DefaultIndexPath = filepath.Join(os.Getenv("HOME"), ".claude", "session-index.jsonl")

// indexEntry represents a single line in session-index.jsonl.
type indexEntry struct {
	SessionID  string `json:"session_id"`
	Transcript string `json:"transcript"`
}

// transcriptLine represents a single line in a transcript JSONL file.
type transcriptLine struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

// userMessage extracts content from a transcript message field.
type userMessage struct {
	Content string `json:"content"`
}

// FindTranscriptPath searches session-index.jsonl for the given session ID
// and returns the transcript file path. Returns empty string if not found.
func FindTranscriptPath(sessionID string) (string, error) {
	return findTranscriptPathFrom(DefaultIndexPath, sessionID)
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
		if tl.Type != "user" {
			continue
		}
		// Extract content from the message field.
		var msg userMessage
		if json.Unmarshal(tl.Message, &msg) != nil {
			continue
		}
		prompt := strings.TrimSpace(msg.Content)
		if prompt != "" {
			return prompt, nil
		}
	}
	return "", nil
}
