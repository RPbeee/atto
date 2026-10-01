package main

import (
	"fmt"
	"strconv"
)

// Buffer text/history is shared; cursor and viewport belong to each visible pane.
type Pane struct {
	Buffer                  *Buffer
	Cursor, Top, Left, Goal int
	views                   map[*Buffer]paneView
}
type paneView struct{ Cursor, Top, Left, Goal int }
type paneRect struct{ x, y, w, h int }

func paneOf(b *Buffer) Pane {
	return Pane{Buffer: b, Cursor: b.Cursor, Top: b.Top, Left: b.Left, Goal: b.goal}
}
func (e *Editor) ensurePanes() {
	if len(e.panes) == 0 && len(e.buffers) > 0 {
		e.panes = []Pane{paneOf(e.current())}
	}
}
func (e *Editor) savePane() {
	if len(e.panes) == 0 || len(e.buffers) == 0 {
		return
	}
	p := &e.panes[e.focused]
	views := p.views
	if views == nil {
		views = make(map[*Buffer]paneView)
	}
	*p = paneOf(e.current())
	p.views = views
	p.views[p.Buffer] = paneView{p.Cursor, p.Top, p.Left, p.Goal}
}
func (e *Editor) setPaneBuffer(b *Buffer) {
	p := &e.panes[e.focused]
	views := p.views
	next := paneOf(b)
	next.views = views
	if saved, ok := views[b]; ok {
		next.Cursor = saved.Cursor
		next.Top = saved.Top
		next.Left = saved.Left
		next.Goal = saved.Goal
	}
	*p = next
	e.restorePane()
}
func (e *Editor) restorePane() {
	p := &e.panes[e.focused]
	b := p.Buffer
	for i, buffer := range e.buffers {
		if buffer == b {
			e.active = i
			break
		}
	}
	b.Cursor = max(0, min(p.Cursor, len(b.Text)))
	b.Top = p.Top
	b.Left = p.Left
	b.goal = p.Goal
	e.clearSelection()
}
func (e *Editor) split(horizontal bool) {
	e.ensurePanes()
	e.savePane()
	e.horizontalSplit = horizontal
	if len(e.panes) == 1 {
		p := e.panes[0]
		p.views = nil
		if len(e.buffers) > 1 {
			p = paneOf(e.buffers[(e.active+1)%len(e.buffers)])
		}
		e.panes = append(e.panes, p)
		e.focused = 1
		e.restorePane()
	}
	e.message = "F7: focus other pane | F8: close pane"
}
func (e *Editor) focusOther() {
	if len(e.panes) < 2 {
		e.message = "Use F3/F4 to split first"
		return
	}
	e.savePane()
	e.focused = 1 - e.focused
	e.restorePane()
}
func (e *Editor) closePane() {
	if len(e.panes) < 2 {
		e.message = "Only one pane"
		return
	}
	e.savePane()
	other := e.panes[1-e.focused]
	e.panes = []Pane{other}
	e.focused = 0
	e.restorePane()
	e.message = "Pane closed; buffers remain open"
}
func (e *Editor) paneRects(w, h int) []paneRect {
	body := paneRect{0, 2, w, max(1, h-5)}
	if len(e.panes) < 2 {
		return []paneRect{body}
	}
	if e.horizontalSplit {
		if body.h < 7 {
			return []paneRect{body}
		}
		first := (body.h - 1) / 2
		return []paneRect{{0, 2, w, first}, {0, 3 + first, w, body.h - first - 1}}
	}
	if w < 49 {
		return []paneRect{body}
	}
	first := (w - 1) / 2
	return []paneRect{{0, 2, first, body.h}, {first + 1, 2, w - first - 1, body.h}}
}
func (e *Editor) pageHeight() int {
	if e.screen == nil {
		return 10
	}
	w, h := e.screen.Size()
	e.ensurePanes()
	rects := e.paneRects(w, h)
	if len(rects) == 2 {
		return max(1, rects[e.focused].h-1)
	}
	return max(1, h-5)
}

// Keep an inactive view near its original text when the shared buffer changes.
func remapCursor(old, next []rune, cursor int) int {
	prefix := 0
	for prefix < len(old) && prefix < len(next) && old[prefix] == next[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(old)-prefix && suffix < len(next)-prefix && old[len(old)-1-suffix] == next[len(next)-1-suffix] {
		suffix++
	}
	if cursor <= prefix {
		return min(cursor, len(next))
	}
	if cursor >= len(old)-suffix {
		return max(0, min(len(next), cursor+len(next)-len(old)))
	}
	return len(next) - suffix
}
func (e *Editor) reconcileViews(b *Buffer, old []rune) {
	if len(e.panes) == 0 {
		return
	}
	changed := len(old) != len(b.Text)
	if !changed {
		for i := range old {
			if old[i] != b.Text[i] {
				changed = true
				break
			}
		}
	}
	if changed {
		for i := range e.panes {
			p := &e.panes[i]
			if i != e.focused && p.Buffer == b {
				p.Cursor = remapCursor(old, b.Text, p.Cursor)
				p.Goal = -1
			}
			if !(i == e.focused && p.Buffer == b) {
				if saved, ok := p.views[b]; ok {
					saved.Cursor = remapCursor(old, b.Text, saved.Cursor)
					saved.Goal = -1
					p.views[b] = saved
				}
			}
		}
	}
	e.savePane()
}
func (e *Editor) drawPane(index int, r paneRect, header bool) {
	p := &e.panes[index]
	b := p.Buffer
	active := index == e.focused
	view := *b
	if !active {
		view.Cursor = min(p.Cursor, len(b.Text))
		view.Top = p.Top
		view.Left = p.Left
		view.goal = p.Goal
	}
	e.paintActive = active
	e.paintSyntax = nil
	if e.syntaxEnabled {
		e.paintSyntax = b.highlight()
	}
	if header {
		style := dimStyle
		marker := " "
		if active {
			style = activeStyle
			marker = ">"
		}
		for x := r.x; x < r.x+r.w; x++ {
			e.screen.SetContent(x, r.y, ' ', nil, style)
		}
		dirty := ""
		if b.dirty() {
			dirty = " *"
		}
		e.text(r.x, r.y, r.w, marker+" "+shortName(b)+dirty, style)
		r.y++
		r.h--
	}
	row, _ := view.rowCol()
	lines := view.lines()
	gutter := lenDigits(len(lines)) + 2
	bodyW := max(1, r.w-gutter)
	if row < view.Top {
		view.Top = row
	}
	if row >= view.Top+r.h {
		view.Top = row - r.h + 1
	}
	view.Top = max(0, min(view.Top, max(0, len(lines)-r.h)))
	start, _ := view.lineBounds()
	cell := displayWidth(string(view.Text[start:view.Cursor]))
	if active && e.rect != nil {
		cell = e.rect.endCol
	}
	if cell < view.Left {
		view.Left = cell
	}
	if cell >= view.Left+bodyW {
		view.Left = cell - bodyW + 1
	}
	offset := 0
	for i, line := range lines {
		if i >= view.Top && i < view.Top+r.h {
			y := r.y + i - view.Top
			e.paintRow = i
			e.text(r.x, y, gutter, lineNumber(i+1, gutter), dimStyle)
			// Paint virtual space too, so rectangles over short lines remain visible.
			if active && e.rect != nil {
				top, bottom, left, right := e.rect.bounds()
				if i >= top && i <= bottom {
					for c := max(left, view.Left); c < min(right, view.Left+bodyW); c++ {
						e.screen.SetContent(r.x+gutter+c-view.Left, y, ' ', nil, selectionStyle)
					}
				}
			}
			e.drawText(r.x+gutter, y, bodyW, view.Left, line, normalStyle, offset)
			if view.Left > 0 {
				e.screen.SetContent(r.x+gutter-1, y, '‹', nil, dimStyle)
			}
			if displayWidth(line) > view.Left+bodyW {
				e.screen.SetContent(r.x+r.w-1, y, '›', nil, dimStyle)
			}
		}
		offset += len([]rune(line)) + 1
	}
	p.Top = view.Top
	p.Left = view.Left
	if active {
		b.Top = view.Top
		b.Left = view.Left
		e.screen.ShowCursor(r.x+gutter+cell-view.Left, r.y+row-view.Top)
	}
	e.paintActive = false
	e.paintSyntax = nil
}
func lenDigits(n int) int             { return len(strconv.Itoa(n)) }
func lineNumber(n, gutter int) string { return fmt.Sprintf("%*d ", gutter-1, n) }
