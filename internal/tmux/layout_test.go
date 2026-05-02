package tmux

import (
	"strings"
	"testing"
)

// ── ParseLayout ──────────────────────────────────────────────────────────────

func TestParseLayout_SinglePane(t *testing.T) {
	// Single-pane window: tmux emits no children.
	in := "abcd,80x24,0,0,1"
	root, err := ParseLayout(in)
	if err != nil {
		t.Fatalf("ParseLayout: %v", err)
	}
	if root.Width != 80 || root.Height != 24 || root.X != 0 || root.Y != 0 {
		t.Errorf("dims: got %+v", root)
	}
	if root.Split != 0 {
		t.Errorf("expected leaf, got split=%c", root.Split)
	}
	if root.PaneID != 1 {
		t.Errorf("PaneID: got %d want 1", root.PaneID)
	}
}

func TestParseLayout_HorizontalThreePane(t *testing.T) {
	in := "4ec6,312x44,0,0{40x44,0,0,32,199x44,41,0,10,71x44,241,0,15}"
	root, err := ParseLayout(in)
	if err != nil {
		t.Fatalf("ParseLayout: %v", err)
	}
	if root.Split != splitHoriz {
		t.Fatalf("expected horizontal split, got %q", root.Split)
	}
	if len(root.Children) != 3 {
		t.Fatalf("children: got %d want 3", len(root.Children))
	}
	want := []struct {
		w, h, x, y, id int
	}{
		{40, 44, 0, 0, 32},
		{199, 44, 41, 0, 10},
		{71, 44, 241, 0, 15},
	}
	for i, c := range root.Children {
		if c.Width != want[i].w || c.Height != want[i].h || c.X != want[i].x || c.Y != want[i].y || c.PaneID != want[i].id {
			t.Errorf("child[%d]: got W=%d H=%d X=%d Y=%d ID=%d want %+v", i, c.Width, c.Height, c.X, c.Y, c.PaneID, want[i])
		}
	}
}

func TestParseLayout_NestedVerticalInsideHorizontal(t *testing.T) {
	// sidebar (40) | column with two stacked panes (rest)
	// outer width = 40 + 1 + 100 = 141; column height splits 22+1+21 = 44
	in := "0000,141x44,0,0{40x44,0,0,32,100x44,41,0[100x22,41,0,10,100x21,41,23,15]}"
	root, err := ParseLayout(in)
	if err != nil {
		t.Fatalf("ParseLayout: %v", err)
	}
	if len(root.Children) != 2 {
		t.Fatalf("outer children: got %d want 2", len(root.Children))
	}
	col := root.Children[1]
	if col.Split != splitVert {
		t.Fatalf("expected vertical split for second child, got %q", col.Split)
	}
	if len(col.Children) != 2 {
		t.Fatalf("col children: got %d want 2", len(col.Children))
	}
}

// ── Round-trip and checksum ──────────────────────────────────────────────────

func TestFormat_RoundTripPreservesString(t *testing.T) {
	cases := []string{
		"4ec6,312x44,0,0{40x44,0,0,32,199x44,41,0,10,71x44,241,0,15}",
		"0000,141x44,0,0{40x44,0,0,32,100x44,41,0[100x22,41,0,10,100x21,41,23,15]}",
	}
	for _, in := range cases {
		root, err := ParseLayout(in)
		if err != nil {
			t.Fatalf("ParseLayout(%q): %v", in, err)
		}
		body := root.formatBody()
		if got, want := body, in[strings.IndexByte(in, ',')+1:]; got != want {
			t.Errorf("body mismatch:\n got: %s\nwant: %s", got, want)
		}
	}
}

func TestLayoutChecksum_KnownVector(t *testing.T) {
	// The user supplied this layout from a real tmux session; the leading "4ec6"
	// is what tmux computed for the body. Recomputing must match.
	in := "4ec6,312x44,0,0{40x44,0,0,32,199x44,41,0,10,71x44,241,0,15}"
	body := in[strings.IndexByte(in, ',')+1:]
	if got := layoutChecksum(body); got != 0x4ec6 {
		t.Errorf("checksum: got %04x want 4ec6", got)
	}
}

func TestFormat_EmitsSameChecksum(t *testing.T) {
	in := "4ec6,312x44,0,0{40x44,0,0,32,199x44,41,0,10,71x44,241,0,15}"
	root, err := ParseLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	if got := root.Format(); got != in {
		t.Errorf("Format: got %s want %s", got, in)
	}
}

// ── RebalanceSidebar ─────────────────────────────────────────────────────────

func TestRebalanceSidebar_DriftedThreePane(t *testing.T) {
	// The user's bad case: sidebar=40, middle=199, right=71. Total inner=310,
	// remaining after sidebar=270, evenly split=135 each.
	in := "4ec6,312x44,0,0{40x44,0,0,32,199x44,41,0,10,71x44,241,0,15}"
	out, ok, err := RebalanceSidebar(in, 32, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	root, err := ParseLayout(out)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	want := []struct{ w, x int }{
		{40, 0},
		{135, 41},
		{135, 177},
	}
	for i, c := range root.Children {
		if c.Width != want[i].w || c.X != want[i].x {
			t.Errorf("child[%d]: got W=%d X=%d want W=%d X=%d", i, c.Width, c.X, want[i].w, want[i].x)
		}
	}
}

func TestRebalanceSidebar_SidebarNotChanged_NoOp(t *testing.T) {
	// Already balanced layout: sidebar=40, two equal 135 panes. After rebalance,
	// dimensions should match (with width=40 enforced).
	in := "0000,312x44,0,0{40x44,0,0,32,135x44,41,0,10,135x44,177,0,15}"
	out, ok, err := RebalanceSidebar(in, 32, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	root, _ := ParseLayout(out)
	for i, c := range root.Children {
		want := []int{40, 135, 135}[i]
		if c.Width != want {
			t.Errorf("child[%d] width: got %d want %d", i, c.Width, want)
		}
	}
}

func TestRebalanceSidebar_TwoPanes(t *testing.T) {
	// sidebar (40) + one other pane (271). Window width = 40 + 1 + 271 = 312.
	in := "0000,312x44,0,0{40x44,0,0,32,271x44,41,0,10}"
	out, ok, err := RebalanceSidebar(in, 32, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	root, _ := ParseLayout(out)
	if root.Children[0].Width != 40 || root.Children[1].Width != 271 {
		t.Errorf("widths: got %d, %d want 40, 271", root.Children[0].Width, root.Children[1].Width)
	}
}

func TestRebalanceSidebar_OnlySidebar_NoOp(t *testing.T) {
	in := "0000,40x44,0,0,32"
	_, ok, _ := RebalanceSidebar(in, 32, 40)
	if ok {
		t.Error("expected ok=false for single-pane layout")
	}
}

func TestRebalanceSidebar_VerticalOuter_NoOp(t *testing.T) {
	in := "0000,80x44,0,0[80x22,0,0,32,80x21,0,23,10]"
	_, ok, _ := RebalanceSidebar(in, 32, 40)
	if ok {
		t.Error("expected ok=false for vertical outer split")
	}
}

func TestRebalanceSidebar_SidebarBuried_NoOp(t *testing.T) {
	// Sidebar inside a nested vertical group — unsupported.
	in := "0000,141x44,0,0{40x44,0,0[40x22,0,0,32,40x21,0,23,9],100x44,41,0,10}"
	_, ok, _ := RebalanceSidebar(in, 32, 40)
	if ok {
		t.Error("expected ok=false when sidebar is not a top-level leaf")
	}
}

func TestRebalanceSidebar_PreservesNestedVertical(t *testing.T) {
	// sidebar | column-with-two-stacked-panes; rebalance must keep the vertical
	// split intact and rescale its width to fill the remainder.
	in := "0000,200x44,0,0{40x44,0,0,32,159x44,41,0[159x22,41,0,10,159x21,41,23,15]}"
	out, ok, err := RebalanceSidebar(in, 32, 40)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("expected ok=true")
	}
	root, _ := ParseLayout(out)
	col := root.Children[1]
	if col.Split != splitVert {
		t.Fatalf("nested split lost: got %q", col.Split)
	}
	// After rebalance: window 200, sidebar 40, sep 1, column = 200 - 40 - 1 = 159.
	if col.Width != 159 {
		t.Errorf("col width: got %d want 159", col.Width)
	}
	for i, c := range col.Children {
		if c.Width != 159 {
			t.Errorf("col.Children[%d].Width = %d, want 159", i, c.Width)
		}
		if c.X != 41 {
			t.Errorf("col.Children[%d].X = %d, want 41", i, c.X)
		}
	}
}
