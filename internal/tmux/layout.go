package tmux

import (
	"fmt"
	"strconv"
	"strings"
)

// LayoutNode is a node in tmux's window layout tree.
//
// tmux's layout-custom.c serialises a window layout as a checksum followed by
// a recursive tree of "WxH,X,Y" nodes. Leaves carry a pane id; horizontal
// splits use {...} and vertical splits use [...]. Children inside a split are
// comma-separated.
type LayoutNode struct {
	Width, Height int
	X, Y          int
	PaneID        int // valid only when Split == 0
	Split         byte
	Children      []*LayoutNode
}

const (
	splitHoriz byte = 'h'
	splitVert  byte = 'v'
)

// ParseLayout parses a tmux layout string of the form "csum,WxH,X,Y..." and
// returns the root node. The checksum is not verified — tmux always emits one
// but we don't depend on it for parsing.
func ParseLayout(s string) (*LayoutNode, error) {
	comma := strings.IndexByte(s, ',')
	if comma < 0 {
		return nil, fmt.Errorf("layout: missing checksum separator")
	}
	body := s[comma+1:]
	node, n, err := parseLayoutNode(body, 0)
	if err != nil {
		return nil, err
	}
	if n != len(body) {
		return nil, fmt.Errorf("layout: trailing input at position %d", n)
	}
	return node, nil
}

func parseLayoutNode(s string, i int) (*LayoutNode, int, error) {
	n := &LayoutNode{PaneID: -1}
	var err error
	if n.Width, i, err = readUint(s, i); err != nil {
		return nil, i, fmt.Errorf("width: %w", err)
	}
	if i >= len(s) || s[i] != 'x' {
		return nil, i, fmt.Errorf("expected 'x' at %d", i)
	}
	i++
	if n.Height, i, err = readUint(s, i); err != nil {
		return nil, i, fmt.Errorf("height: %w", err)
	}
	if i >= len(s) || s[i] != ',' {
		return nil, i, fmt.Errorf("expected ',' after height at %d", i)
	}
	i++
	if n.X, i, err = readUint(s, i); err != nil {
		return nil, i, fmt.Errorf("x: %w", err)
	}
	if i >= len(s) || s[i] != ',' {
		return nil, i, fmt.Errorf("expected ',' after x at %d", i)
	}
	i++
	if n.Y, i, err = readUint(s, i); err != nil {
		return nil, i, fmt.Errorf("y: %w", err)
	}
	if i >= len(s) {
		// A bare "WxH,X,Y" with no id and no children — treat as a leaf with no id.
		return n, i, nil
	}
	switch s[i] {
	case '{':
		return parseChildren(n, s, i+1, splitHoriz, '}')
	case '[':
		return parseChildren(n, s, i+1, splitVert, ']')
	case ',':
		// Leaf: ",<id>"
		i++
		if n.PaneID, i, err = readUint(s, i); err != nil {
			return nil, i, fmt.Errorf("pane id: %w", err)
		}
		return n, i, nil
	default:
		// End of parent group reached without an id; let caller handle it.
		return n, i, nil
	}
}

func parseChildren(n *LayoutNode, s string, i int, split byte, closer byte) (*LayoutNode, int, error) {
	n.Split = split
	for {
		child, ni, err := parseLayoutNode(s, i)
		if err != nil {
			return nil, ni, err
		}
		n.Children = append(n.Children, child)
		i = ni
		if i >= len(s) {
			return nil, i, fmt.Errorf("unterminated split (expected %q)", closer)
		}
		switch s[i] {
		case ',':
			i++
			continue
		case closer:
			return n, i + 1, nil
		default:
			return nil, i, fmt.Errorf("unexpected %q in split", s[i])
		}
	}
}

func readUint(s string, i int) (int, int, error) {
	start := i
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == start {
		return 0, i, fmt.Errorf("expected digit at %d", i)
	}
	v, err := strconv.Atoi(s[start:i])
	if err != nil {
		return 0, i, err
	}
	return v, i, nil
}

// Format serialises the layout tree to tmux's "csum,body" form.
func (n *LayoutNode) Format() string {
	body := n.formatBody()
	return fmt.Sprintf("%04x,%s", layoutChecksum(body), body)
}

func (n *LayoutNode) formatBody() string {
	var b strings.Builder
	n.writeBody(&b)
	return b.String()
}

func (n *LayoutNode) writeBody(b *strings.Builder) {
	fmt.Fprintf(b, "%dx%d,%d,%d", n.Width, n.Height, n.X, n.Y)
	switch n.Split {
	case splitHoriz, splitVert:
		open, close := byte('{'), byte('}')
		if n.Split == splitVert {
			open, close = '[', ']'
		}
		b.WriteByte(open)
		for i, c := range n.Children {
			if i > 0 {
				b.WriteByte(',')
			}
			c.writeBody(b)
		}
		b.WriteByte(close)
	default:
		fmt.Fprintf(b, ",%d", n.PaneID)
	}
}

// layoutChecksum reproduces tmux's layout_checksum from layout-custom.c:
//
//	csum = (csum >> 1) + ((csum & 1) << 15)
//	csum += *layout
//
// It is a 16-bit rotate-right-by-1 then add the byte.
func layoutChecksum(body string) uint16 {
	var csum uint16
	for i := 0; i < len(body); i++ {
		csum = (csum >> 1) | ((csum & 1) << 15)
		csum += uint16(body[i])
	}
	return csum
}

// RecomputePositions walks the tree and assigns X/Y to every descendant based
// on the parent's origin and sibling cumulative offsets (with a +1 separator
// between adjacent panes in a split).
func (n *LayoutNode) RecomputePositions() {
	n.recomputePos(n.X, n.Y)
}

func (n *LayoutNode) recomputePos(x, y int) {
	n.X = x
	n.Y = y
	switch n.Split {
	case splitHoriz:
		cx := x
		for _, c := range n.Children {
			c.recomputePos(cx, y)
			cx += c.Width + 1
		}
	case splitVert:
		cy := y
		for _, c := range n.Children {
			c.recomputePos(x, cy)
			cy += c.Height + 1
		}
	}
}

// SetWidth rescales this node and its descendants to a new width.
//
// For horizontal splits the new inner width (newW minus separators) is
// distributed across children proportionally to their existing widths, with
// the rounding error absorbed by the last child.
//
// For vertical splits every child gets the new width.
//
// Leaves just adopt the new width.
func (n *LayoutNode) SetWidth(w int) {
	if w == n.Width {
		return
	}
	switch n.Split {
	case splitHoriz:
		oldInner := n.Width - (len(n.Children) - 1)
		newInner := w - (len(n.Children) - 1)
		n.Width = w
		if oldInner <= 0 || newInner < len(n.Children) {
			equal := newInner / len(n.Children)
			if equal < 1 {
				equal = 1
			}
			for _, c := range n.Children {
				c.SetWidth(equal)
			}
			return
		}
		assigned := 0
		for i, c := range n.Children {
			var nw int
			if i == len(n.Children)-1 {
				nw = newInner - assigned
			} else {
				nw = c.Width * newInner / oldInner
				if nw < 1 {
					nw = 1
				}
			}
			c.SetWidth(nw)
			assigned += nw
		}
	case splitVert:
		n.Width = w
		for _, c := range n.Children {
			c.SetWidth(w)
		}
	default:
		n.Width = w
	}
}

// SetHeight is the vertical counterpart of SetWidth.
func (n *LayoutNode) SetHeight(h int) {
	if h == n.Height {
		return
	}
	switch n.Split {
	case splitVert:
		oldInner := n.Height - (len(n.Children) - 1)
		newInner := h - (len(n.Children) - 1)
		n.Height = h
		if oldInner <= 0 || newInner < len(n.Children) {
			equal := newInner / len(n.Children)
			if equal < 1 {
				equal = 1
			}
			for _, c := range n.Children {
				c.SetHeight(equal)
			}
			return
		}
		assigned := 0
		for i, c := range n.Children {
			var nh int
			if i == len(n.Children)-1 {
				nh = newInner - assigned
			} else {
				nh = c.Height * newInner / oldInner
				if nh < 1 {
					nh = 1
				}
			}
			c.SetHeight(nh)
			assigned += nh
		}
	case splitHoriz:
		n.Height = h
		for _, c := range n.Children {
			c.SetHeight(h)
		}
	default:
		n.Height = h
	}
}

// RebalanceSidebar rewrites a window layout so the sidebar pane has the given
// width and the remaining horizontal space is split evenly across the other
// top-level panes.
//
// Returns the new layout string and ok=true when rebalancing succeeded.
// Returns ok=false when the layout is unsupported (sidebar is the only pane,
// the outer split is not horizontal, the sidebar is buried in a nested split,
// or there is not enough room to fit the sidebar plus one column per other
// pane). In the unsupported case the caller should fall back to a plain
// `resize-pane -x` so the sidebar at least keeps its configured width.
func RebalanceSidebar(layoutStr string, sidebarPaneNumber, sidebarWidth int) (string, bool, error) {
	root, err := ParseLayout(layoutStr)
	if err != nil {
		return "", false, err
	}
	if root.Split != splitHoriz {
		return "", false, nil
	}
	sidebarIdx := -1
	for i, c := range root.Children {
		if c.Split == 0 && c.PaneID == sidebarPaneNumber {
			sidebarIdx = i
			break
		}
	}
	if sidebarIdx == -1 {
		return "", false, nil
	}
	if len(root.Children) < 2 {
		return "", false, nil
	}

	innerTotal := root.Width - (len(root.Children) - 1)
	nonSidebarCount := len(root.Children) - 1
	remaining := innerTotal - sidebarWidth
	if remaining < nonSidebarCount {
		return "", false, nil
	}

	root.Children[sidebarIdx].SetWidth(sidebarWidth)
	base := remaining / nonSidebarCount
	extra := remaining - base*nonSidebarCount
	for i, c := range root.Children {
		if i == sidebarIdx {
			continue
		}
		w := base
		if extra > 0 {
			w++
			extra--
		}
		c.SetWidth(w)
	}
	root.RecomputePositions()
	return root.Format(), true, nil
}
