package main

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"
)

type rectangle struct{ anchorRow, anchorCol, endRow, endCol int }

func (r *rectangle) bounds() (int, int, int, int) {
	return min(r.anchorRow, r.endRow), max(r.anchorRow, r.endRow), min(r.anchorCol, r.endCol), max(r.anchorCol, r.endCol)
}
func (e *Editor) clearSelection() { e.mark = -1; e.rect = nil }
func (b *Buffer) cursorCell() int {
	start, _ := b.lineBounds()
	return displayWidth(string(b.Text[start:b.Cursor]))
}
func (b *Buffer) goToCell(row, col int) {
	lines := b.lines()
	row = max(0, min(row, len(lines)-1))
	b.goTo(row+1, runeAtCell(lines[row], col)+1)
}
func (e *Editor) toggleRectangle() {
	if e.rect != nil {
		e.clearSelection()
		e.message = "Rectangle cleared"
		return
	}
	b := e.current()
	row, _ := b.rowCol()
	col := b.cursorCell()
	e.mark = -1
	e.rect = &rectangle{row, col, row, col}
	e.message = "Rectangle: move with arrows, type/Cut/Copy; Esc finishes"
}

// Slice by terminal cells without splitting Unicode clusters. Partial wide
// characters become spaces; tabs in edited rectangular rows expand to spaces.
func cellSlice(line string, lo, hi int, pad bool) string {
	var out strings.Builder
	col := 0
	g := uniseg.NewGraphemes(line)
	for g.Next() {
		cluster := g.Str()
		width := cellWidth(cluster, col)
		end := col + width
		if width == 0 {
			if col >= lo && col < hi {
				out.WriteString(cluster)
			}
		} else if col < hi && end > lo {
			overlap := min(end, hi) - max(col, lo)
			if cluster == "\t" || overlap != width {
				out.WriteString(strings.Repeat(" ", overlap))
			} else {
				out.WriteString(cluster)
			}
		}
		col = end
		if col >= hi {
			break
		}
	}
	if pad && col < hi {
		out.WriteString(strings.Repeat(" ", hi-max(lo, col)))
	}
	return out.String()
}
func (b *Buffer) copyRectangle(top, bottom, left, right int) []string {
	lines := b.lines()
	out := make([]string, 0, bottom-top+1)
	for row := top; row <= bottom; row++ {
		line := ""
		if row < len(lines) {
			line = lines[row]
		}
		out = append(out, cellSlice(line, left, right, true))
	}
	return out
}

// Replace all target rows as one edit/undo operation. A single source row is
// repeated; multiple source rows map one-to-one and may extend the document.
func expandTabs(s string, start int) string {
	var out strings.Builder
	col := start
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		cluster := g.Str()
		width := cellWidth(cluster, col)
		if cluster == "\t" {
			out.WriteString(strings.Repeat(" ", width))
		} else {
			out.WriteString(cluster)
		}
		col += width
	}
	return out.String()
}
func (b *Buffer) replaceRectangle(top, bottom, left, right int, source []string) (int, int) {
	if len(source) == 0 {
		source = []string{""}
	}
	lines := b.lines()
	if len(source) > 1 {
		bottom = max(bottom, top+len(source)-1)
	}
	for len(lines) <= bottom {
		lines = append(lines, "")
	}
	lastCol := left
	for row := top; row <= bottom; row++ {
		insert := ""
		if len(source) == 1 {
			insert = source[0]
		} else if row-top < len(source) {
			insert = source[row-top]
		}
		insert = expandTabs(insert, left)
		if insert == "" && (left == right || left >= displayWidth(lines[row])) {
			if row == bottom {
				lastCol = left
			}
			continue
		}
		// A deletion outside a short line should leave it short. Insertion pads it.
		pad := insert != ""
		before := cellSlice(lines[row], 0, left, pad)
		after := cellSlice(lines[row], right, displayWidth(lines[row]), false)
		lines[row] = before + insert + after
		if row == bottom {
			lastCol = left + displayWidth(insert)
		}
	}
	b.edit(0, len(b.Text), strings.Join(lines, "\n"))
	b.goToCell(bottom, lastCol)
	return bottom, lastCol
}
func (e *Editor) rectangleInsert(s string) {
	top, bottom, left, right := e.rect.bounds()
	rows := strings.Split(s, "\n")
	bottom, col := e.current().replaceRectangle(top, bottom, left, right, rows)
	// Keep zero-width selection across the same rows for repeated column typing.
	e.rect = &rectangle{top, col, bottom, col}
	e.mark = -1
}
func (e *Editor) rectangleDelete(backward bool) {
	top, bottom, left, right := e.rect.bounds()
	if left == right {
		if backward {
			if left == 0 {
				return
			}
			left--
		} else {
			right++
		}
	}
	e.current().replaceRectangle(top, bottom, left, right, []string{""})
	e.rect = &rectangle{top, left, bottom, left}
}
func (e *Editor) rectangleMove(key tcell.Key) {
	b := e.current()
	r := e.rect
	lines := b.lines()
	switch key {
	case tcell.KeyLeft:
		r.endCol = max(0, r.endCol-1)
	case tcell.KeyRight:
		r.endCol++
	case tcell.KeyUp:
		r.endRow = max(0, r.endRow-1)
	case tcell.KeyDown:
		r.endRow = min(len(lines)-1, r.endRow+1)
	case tcell.KeyPgUp:
		r.endRow = max(0, r.endRow-e.pageHeight())
	case tcell.KeyPgDn:
		r.endRow = min(len(lines)-1, r.endRow+e.pageHeight())
	case tcell.KeyHome, tcell.KeyCtrlA:
		r.endCol = 0
	case tcell.KeyEnd, tcell.KeyCtrlE:
		r.endCol = displayWidth(lines[r.endRow])
	}
	b.goToCell(r.endRow, r.endCol)
}
func (e *Editor) pasteClipboard() {
	if e.rectClipboard == nil {
		e.insert(e.clipboard)
		return
	}
	b := e.current()
	if e.rect != nil {
		e.rectangleInsert(strings.Join(e.rectClipboard, "\n"))
		return
	}
	// Rectangular clipboard is inserted at the cursor column on successive rows.
	row, _ := b.rowCol()
	col := b.cursorCell()
	b.replaceRectangle(row, row, col, col, e.rectClipboard)
	e.clearSelection()
}
func (e *Editor) rectangleCut(copyOnly bool) {
	top, bottom, left, right := e.rect.bounds()
	if left == right {
		e.message = "Rectangle has zero width"
		return
	}
	e.rectClipboard = e.current().copyRectangle(top, bottom, left, right)
	e.clipboard = strings.Join(e.rectClipboard, "\n")
	if !copyOnly {
		e.current().replaceRectangle(top, bottom, left, right, []string{""})
	}
	e.clearSelection()
	e.message = "Rectangle copied"
	if !copyOnly {
		e.message = "Rectangle cut"
	}
}
