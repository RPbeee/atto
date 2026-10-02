package main

import (
	"fmt"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

var (
	normalStyle    = tcell.StyleDefault.Foreground(tcell.ColorWhite).Background(tcell.ColorBlack)
	barStyle       = tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorTeal)
	activeStyle    = barStyle.Bold(true).Background(tcell.ColorWhite)
	dimStyle       = normalStyle.Foreground(tcell.ColorGray)
	selectionStyle = normalStyle.Background(tcell.ColorDarkCyan)
	messageStyle   = normalStyle.Foreground(tcell.ColorYellow)
)

// drawText renders grapheme clusters, tabs and control characters as cells.
// left is a display-cell offset; partially clipped wide characters are blanked.
func (e *Editor) drawText(x, y, width, left int, s string, style tcell.Style, offset int) {
	g := uniseg.NewGraphemes(s)
	col, pos := 0, offset
	start, end, selected := 0, 0, false
	if offset >= 0 && e.paintActive {
		start, end, selected = e.selection()
	}
	for g.Next() {
		cluster := g.Str()
		cw := cellWidth(cluster, col)
		st := style
		if offset >= 0 {
			st = e.paintSyntax.styleAt(pos, style)
		}
		baseStyle := st
		if offset >= 0 && e.paintActive && e.rect != nil {
			top, bottom, left, right := e.rect.bounds()
			if e.paintRow >= top && e.paintRow <= bottom && col < right && col+cw > left {
				st = st.Background(tcell.ColorDarkCyan)
			}
		}
		if selected && pos < end && pos+utf8.RuneCountInString(cluster) > start {
			st = st.Background(tcell.ColorDarkCyan)
		}
		visible := col - left
		if visible >= width {
			break
		}
		if visible+cw > 0 {
			switch {
			case cluster == "\t":
				for i := 0; i < cw; i++ {
					if visible+i >= 0 && visible+i < width {
						cellStyle := st
						if offset >= 0 && e.paintActive && e.rect != nil {
							top, bottom, rl, rr := e.rect.bounds()
							cellStyle = baseStyle
							if e.paintRow >= top && e.paintRow <= bottom && col+i >= rl && col+i < rr {
								cellStyle = baseStyle.Background(tcell.ColorDarkCyan)
							}
						}
						e.screen.SetContent(x+visible+i, y, ' ', nil, cellStyle)
					}
				}
			case len(cluster) == 1 && (cluster[0] < 32 || cluster[0] == 127):
				ch := rune(cluster[0]) + 64
				if cluster[0] == 127 {
					ch = '?'
				}
				for i, r := range []rune{'^', ch} {
					if visible+i >= 0 && visible+i < width {
						e.screen.SetContent(x+visible+i, y, r, nil, st)
					}
				}
			default:
				if visible >= 0 && visible+cw <= width {
					runes := []rune(cluster)
					if cw == 0 { /* zero-width controls have no terminal effect */
					} else {
						e.screen.SetContent(x+visible, y, runes[0], runes[1:], st)
					}
				}
			}
		}
		col += cw
		pos += utf8.RuneCountInString(cluster)
	}
}
func (e *Editor) text(x, y, width int, s string, style tcell.Style) {
	if width > 0 {
		e.drawText(x, y, width, 0, s, style, -1)
	}
}
func (e *Editor) bar(y, w int, style tcell.Style) {
	for x := 0; x < w; x++ {
		e.screen.SetContent(x, y, ' ', nil, style)
	}
}
func (e *Editor) draw() {
	s := e.screen
	s.SetStyle(normalStyle)
	s.Clear()
	s.HideCursor()
	w, h := s.Size()
	e.pollProject()
	e.ensurePanes()
	e.savePane()
	if len(e.buffers) == 0 {
		s.Show()
		return
	}
	if w < 24 || h < 8 {
		e.text(0, 0, w, "Resize terminal: minimum 24x8", messageStyle)
		s.Show()
		return
	}
	b := e.current()
	row, col := b.rowCol()
	dirty := ""
	if b.dirty() {
		dirty = " *"
	}
	e.bar(0, w, barStyle)
	e.text(0, 0, w, fmt.Sprintf(" atto | %s%s", b.name(), dirty), barStyle)
	e.bar(1, w, barStyle)
	// Scroll the tab strip so the current tab is always visible, even with many files.
	tabs := make([]string, len(e.buffers))
	before := 0
	for i, buf := range e.buffers {
		marker := ""
		if buf.dirty() {
			marker = "*"
		}
		tabs[i] = fmt.Sprintf(" %d:%s%s ", i+1, shortName(buf), marker)
		if i < e.active {
			before += displayWidth(tabs[i]) + 1
		}
	}
	tabLeft := max(0, before+min(displayWidth(tabs[e.active]), w)-w)
	tabX := -tabLeft
	for i, tab := range tabs {
		st := barStyle
		if i == e.active {
			st = activeStyle
		}
		cw := displayWidth(tab)
		if tabX < w && tabX+cw > 0 {
			for x := max(0, tabX); x < min(w, tabX+cw); x++ {
				s.SetContent(x, 1, ' ', nil, st)
			}
			e.drawText(max(0, tabX), 1, w-max(0, tabX), max(0, -tabX), tab, st, -1)
		}
		tabX += cw + 1
	}
	rects := e.paneRects(w, h)
	if len(rects) == 1 {
		e.drawPane(e.focused, rects[0], false)
	} else {
		for i, r := range rects {
			e.drawPane(i, r, true)
		}
		if e.horizontalSplit {
			y := rects[1].y - 1
			for x := 0; x < w; x++ {
				s.SetContent(x, y, '─', nil, dimStyle)
			}
		} else {
			x := rects[1].x - 1
			for y := 2; y < h-3; y++ {
				s.SetContent(x, y, '│', nil, dimStyle)
			}
		}
	}
	e.savePane()
	e.bar(h-3, w, barStyle)
	eol := "LF"
	if b.EOL == "\r\n" {
		eol = "CRLF"
	}
	mode := ""
	if e.syntaxEnabled {
		syntax := b.highlight()
		mode = " " + syntax.language
		if syntax.skipped {
			mode += " (plain)"
		}
	} else {
		mode = " syntax off"
	}
	if e.rect != nil {
		mode += " RECT"
	}
	if len(e.panes) > 1 {
		mode += fmt.Sprintf(" Pane %d/2", e.focused+1)
		if len(rects) == 1 {
			mode += " (resize for split)"
		}
	}
	e.text(0, h-3, w, fmt.Sprintf(" %d/%d | Ln %d, Col %d | %s%s | %s", e.active+1, len(e.buffers), row+1, col+1, eol, mode, e.message), barStyle)
	e.text(0, h-2, w, "^G Help  ^S Save  ^O Save As  ^R Open  ^N New  ^X Close  ^Q Quit", messageStyle)
	e.text(0, h-1, w, "F3/F4 Split  F7 Focus  F8 Unsplit  ^P Project  Alt-R Rectangle", messageStyle)
	if e.help {
		e.drawHelp(w, h)
	}
	if e.listing {
		e.drawList(w, h)
	}
	if e.projectView {
		e.drawProject(w, h)
	}
	if e.explorer != nil {
		e.drawExplorer(w, h)
	}
	if e.prompt != nil {
		p := e.prompt
		e.bar(h-3, w, activeStyle)
		label := p.label + " "
		prefixWidth := min(displayWidth(label), w/2)
		e.text(0, h-3, prefixWidth, label, activeStyle)
		if !p.confirm {
			cell := displayWidth(string(p.text[:p.cursor]))
			left := max(0, cell-(w-prefixWidth)+1)
			e.drawText(prefixWidth, h-3, w-prefixWidth, left, string(p.text), activeStyle, -1)
			s.ShowCursor(prefixWidth+cell-left, h-3)
		} else {
			e.text(0, h-3, w, label, activeStyle)
			s.HideCursor()
		}
	}
	s.Show()
}
func (e *Editor) drawHelp(w, h int) {
	e.screen.Clear()
	e.screen.HideCursor()
	e.bar(0, w, barStyle)
	e.text(0, 0, w, " atto — Help (any key returns)", barStyle)
	help := []string{
		"Ctrl-S Save             Ctrl-O Save As (choose path)",
		"Ctrl-R Open (file explorer; h/j/k/l, Enter, ~ type path)   Ctrl-N New",
		"Ctrl-X Close buffer     Ctrl-Q Quit all buffers",
		"F5 / Alt-[ Previous     F6 / Alt-] Next buffer",
		"Ctrl-B Buffer list      F2 Save all modified buffers",
		"Arrows Move             Home/End or Ctrl-A/E Line start/end",
		"PgUp/PgDn Page          Ctrl-J Go to line[:column]",
		"Ctrl-W Search           Alt-W Next match (wraps)",
		"Ctrl-\\ Replace all      Alt-U Undo / Alt-E Redo",
		"Ctrl-Space / Ctrl-6 Toggle selection; Shift+arrows select",
		"Ctrl-K Cut line/selection   Alt-6 Copy line/selection",
		"Ctrl-U Paste internal clipboard   Esc Clear selection",
		"Prompts: Enter accept, Esc/Ctrl-C cancel, Ctrl-U clear",
		"Unsaved close/quit: y saves, n discards, Esc cancels",
		"* = modified; each buffer keeps its own cursor and history",
		"UTF-8, LF/CRLF and BOM supported; tabs display at 4 cells",
		"F3/F4 Split left-right/top-bottom; F7/Alt-O Focus; F8 Close pane",
		"Ctrl-P Project search (directory then query); Alt-P Results",
		"Alt-R Rectangle; arrows select columns/rows; typing edits all",
		"Rectangle: Ctrl-K/Alt-6 Cut/Copy, Ctrl-U Paste, Esc Finish",
		"F9/Alt-H Syntax on/off; Alt-L Choose language (auto/off/go/...)",
	}
	for i, line := range help {
		if i+2 >= h {
			break
		}
		e.text(0, i+2, w, line, normalStyle)
	}
}
func (e *Editor) drawList(w, h int) {
	e.screen.Clear()
	e.screen.HideCursor()
	e.bar(0, w, barStyle)
	e.text(0, 0, w, " Buffers — Up/Down, Enter selects, Esc cancels", barStyle)
	top := max(0, e.listIndex-(h-3)+1)
	for i := top; i < len(e.buffers) && i-top < h-2; i++ {
		b := e.buffers[i]
		st := normalStyle
		if i == e.listIndex {
			st = activeStyle
			e.bar(i-top+2, w, st)
		}
		marker := " "
		if b.dirty() {
			marker = "*"
		}
		e.text(0, i-top+2, w, fmt.Sprintf("%s %d %s", marker, i+1, b.name()), st)
	}
}
