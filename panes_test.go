package main

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestSplitBuffersAndIndependentViews(t *testing.T) {
	e := testEditor(t)
	typed(e, "first\nline")
	first := e.current()
	key(e, tcell.KeyCtrlN)
	typed(e, "second")
	second := e.current()
	e.switchTo(0)
	key(e, tcell.KeyF3)
	if len(e.panes) != 2 || e.focused != 1 || e.current() != second || e.panes[0].Buffer != first {
		t.Fatal("split did not show next buffer")
	}
	typed(e, "!")
	key(e, tcell.KeyF7)
	if e.current() != first || first.Cursor != len(first.Text) {
		t.Fatal("focus state lost")
	}
	key(e, tcell.KeyF4)
	if !e.horizontalSplit || len(e.panes) != 2 {
		t.Fatal("orientation")
	}
	key(e, tcell.KeyF8)
	if len(e.panes) != 1 || len(e.buffers) != 2 || e.current() != second || !strings.HasSuffix(string(second.Text), "!") {
		t.Fatal("closing pane closed buffer")
	}
}
func TestSameBufferViewsShareTextNotCursor(t *testing.T) {
	e := testEditor(t)
	typed(e, "abc\ndef\nghi")
	b := e.current()
	key(e, tcell.KeyHome)
	old := b.Cursor
	key(e, tcell.KeyF3)
	key(e, tcell.KeyUp)
	key(e, tcell.KeyHome)
	typed(e, "X")
	if string(b.Text) != "abc\nXdef\nghi" {
		t.Fatal(string(b.Text))
	}
	key(e, tcell.KeyF7)
	if b.Cursor != old+1 {
		t.Fatal("inactive cursor failed to follow text", b.Cursor, old)
	}
	key(e, tcell.KeyF7)
	if b.Cursor != 5 {
		t.Fatal("active view cursor lost", b.Cursor)
	}
	alt(e, 'u')
	if string(b.Text) != "abc\ndef\nghi" {
		t.Fatal("shared undo")
	}
	key(e, tcell.KeyF7)
	if b.Cursor != old {
		t.Fatal("undo remapping", b.Cursor)
	}
	// Each pane also remembers its own position when switching away and back.
	key(e, tcell.KeyCtrlN)
	e.switchTo(0)
	if b.Cursor != old {
		t.Fatal("pane's buffer position lost", b.Cursor)
	}
}
func TestCloseBufferReassignsEveryPane(t *testing.T) {
	e := testEditor(t)
	first := e.current()
	key(e, tcell.KeyCtrlN)
	second := e.current()
	e.switchTo(0)
	key(e, tcell.KeyF3)
	e.switchTo(0)
	if e.panes[0].Buffer != first || e.panes[1].Buffer != first {
		t.Fatal("same buffer setup")
	}
	key(e, tcell.KeyCtrlX)
	if len(e.buffers) != 1 || e.current() != second {
		t.Fatal("close buffer")
	}
	for _, p := range e.panes {
		if p.Buffer != second {
			t.Fatal("dangling closed buffer")
		}
	}
	key(e, tcell.KeyF7)
	e.draw()
}
func TestSplitRenderingAndResize(t *testing.T) {
	e := testEditor(t)
	typed(e, "left")
	key(e, tcell.KeyCtrlN)
	typed(e, "right")
	e.switchTo(0)
	key(e, tcell.KeyF3)
	e.draw()
	screen := e.screen.(tcell.SimulationScreen)
	r, _, _, _ := screen.GetContent(39, 3)
	if r != '│' {
		t.Fatal("vertical separator", r)
	}
	r, _, _, _ = screen.GetContent(3, 3)
	if r != 'l' {
		t.Fatal("left pane", r)
	}
	r, _, _, _ = screen.GetContent(43, 3)
	if r != 'r' {
		t.Fatal("right pane", r)
	}
	key(e, tcell.KeyF4)
	e.draw()
	rects := e.paneRects(80, 24)
	r, _, _, _ = screen.GetContent(0, rects[1].y-1)
	if r != '─' {
		t.Fatal("horizontal separator")
	}
	screen.SetSize(24, 8)
	e.draw()
	if len(e.panes) != 2 || len(e.paneRects(24, 8)) != 1 {
		t.Fatal("small screen discarded split")
	}
	screen.SetSize(80, 24)
	e.draw()
	if len(e.paneRects(80, 24)) != 2 {
		t.Fatal("split not restored")
	}
}
