package picker

import (
	"encoding/json"
	"fmt"
	"os"
)

// Context is the JSON payload pane mode writes to a temp file before launching
// the picker, so the popup can detect duplicate sessions and dim them.
type Context struct {
	// Sessions is the list of currently open tmux sessions. Used to detect
	// when the user picks a repo whose session already exists, in which case
	// the picker should switch to it instead of creating a new one.
	Sessions []SessionInfo `json:"sessions"`
	// Pinned mirrors the user's pinned_sessions config. Currently unused by
	// the picker UI but kept for forward compatibility (e.g. annotating
	// pinned sessions in the repo list).
	Pinned []string `json:"pinned"`
	// SidebarSessionID is the session id of the sidebar pane that launched
	// the picker (e.g. "$3"). Used so the picker does not switch to its own
	// session by mistake when the user chose the matching repo.
	SidebarSessionID string `json:"sidebar_session_id"`
}

// SessionInfo carries the tmux session name plus its working directory so the
// picker can match by name and warn when a session was created in a different
// path than the picked repo.
type SessionInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// WriteContext serialises ctx to path. Used by pane mode before launching the
// popup.
func WriteContext(path string, ctx Context) error {
	data, err := json.Marshal(ctx)
	if err != nil {
		return fmt.Errorf("encode context: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write context: %w", err)
	}
	return nil
}

// ReadContext deserialises the file written by WriteContext. Missing file
// yields an empty Context (no error), so the picker still runs even when the
// caller forgot to pass --context.
func ReadContext(path string) (Context, error) {
	if path == "" {
		return Context{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Context{}, nil
		}
		return Context{}, fmt.Errorf("read context: %w", err)
	}
	var ctx Context
	if err := json.Unmarshal(data, &ctx); err != nil {
		return Context{}, fmt.Errorf("parse context: %w", err)
	}
	return ctx, nil
}
