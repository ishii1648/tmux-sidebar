package picker

import (
	"path/filepath"
	"testing"
)

func TestWriteReadContext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ctx.json")
	want := Context{
		Sessions: []SessionInfo{
			{Name: "foo", Path: "/tmp/foo"},
			{Name: "bar", Path: "/tmp/bar"},
		},
		Pinned:           []string{"foo"},
		SidebarSessionID: "$3",
	}
	if err := WriteContext(path, want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := ReadContext(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.SidebarSessionID != want.SidebarSessionID {
		t.Errorf("sid = %q want %q", got.SidebarSessionID, want.SidebarSessionID)
	}
	if len(got.Sessions) != 2 || got.Sessions[1].Name != "bar" {
		t.Errorf("sessions = %+v", got.Sessions)
	}
	if len(got.Pinned) != 1 || got.Pinned[0] != "foo" {
		t.Errorf("pinned = %+v", got.Pinned)
	}
}

func TestReadContextMissing(t *testing.T) {
	got, err := ReadContext("/tmp/does-not-exist-xxx.json")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(got.Sessions) != 0 {
		t.Errorf("expected empty context, got %+v", got)
	}
}

func TestReadContextEmptyPath(t *testing.T) {
	got, err := ReadContext("")
	if err != nil || len(got.Sessions) != 0 {
		t.Errorf("ReadContext(\"\") = (%+v, %v)", got, err)
	}
}
