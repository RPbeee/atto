package main

import (
	"strings"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

const historyLimit = 200
const historyBytes = 16 << 20
const tabWidth = 4

type snapshot struct {
	text   string
	cursor int
}

// Each buffer owns its history and viewport. Positions are rune offsets.
type Buffer struct {
	Path           string
	Text           []rune
	Cursor         int
	Top, Left      int
	goal           int
	saved          string
	disk           diskState
	EOL            string
	BOM            bool
	undo, redo     []snapshot
	SyntaxLanguage string
	revision       uint64
	syntax         syntaxCache
}

func newBuffer() *Buffer      { return &Buffer{EOL: "\n", goal: -1} }
func (b *Buffer) dirty() bool { return string(b.Text) != b.saved }
func (b *Buffer) name() string {
	if b.Path == "" {
		return "[untitled]"
	}
	return b.Path
}
func (b *Buffer) state() snapshot { return snapshot{string(b.Text), b.Cursor} }
func boundedHistory(h []snapshot, s snapshot) []snapshot {
	h = append(h, s)
	total := 0
	for i := len(h) - 1; i >= 0; i-- {
		total += len(h[i].text)
		if total > historyBytes || len(h)-i > historyLimit {
			// Release discarded strings even while the backing array is retained.
			clear(h[:i+1])
			return h[i+1:]
		}
	}
	return h
}
func (b *Buffer) restore(s snapshot) {
	b.Text = []rune(s.text)
	b.Cursor = s.cursor
	b.goal = -1
	b.revision++
}
func (b *Buffer) edit(start, end int, text string) {
	if start < 0 || end < start || end > len(b.Text) {
		return
	}
	if string(b.Text[start:end]) == text {
		return
	}
	b.undo = boundedHistory(b.undo, b.state())
	b.redo = nil
	insert := []rune(text)
	next := make([]rune, 0, len(b.Text)-(end-start)+len(insert))
	next = append(next, b.Text[:start]...)
	next = append(next, insert...)
	next = append(next, b.Text[end:]...)
	b.Text = next
	b.revision++
	b.Cursor = start + len(insert)
	b.goal = -1
}
func (b *Buffer) insert(s string) { b.edit(b.Cursor, b.Cursor, s) }
func (b *Buffer) undoEdit() {
	if len(b.undo) == 0 {
		return
	}
	s := b.undo[len(b.undo)-1]
	b.undo[len(b.undo)-1] = snapshot{}
	b.undo = b.undo[:len(b.undo)-1]
	b.redo = boundedHistory(b.redo, b.state())
	b.restore(s)
}
func (b *Buffer) redoEdit() {
	if len(b.redo) == 0 {
		return
	}
	s := b.redo[len(b.redo)-1]
	b.redo[len(b.redo)-1] = snapshot{}
	b.redo = b.redo[:len(b.redo)-1]
	b.undo = boundedHistory(b.undo, b.state())
	b.restore(s)
}
func (b *Buffer) lineBounds() (int, int) {
	start, end := b.Cursor, b.Cursor
	for start > 0 && b.Text[start-1] != '\n' {
		start--
	}
	for end < len(b.Text) && b.Text[end] != '\n' {
		end++
	}
	return start, end
}
func (b *Buffer) rowCol() (int, int) {
	row, start := 0, 0
	for i := 0; i < b.Cursor; i++ {
		if b.Text[i] == '\n' {
			row++
			start = i + 1
		}
	}
	return row, b.Cursor - start
}
func (b *Buffer) lines() []string { return strings.Split(string(b.Text), "\n") }

// Grapheme boundaries keep combining marks and emoji together when moving/deleting.
func boundary(s string, pos int, forward bool) int {
	g := uniseg.NewGraphemes(s)
	offset, prev := 0, 0
	for g.Next() {
		offset += utf8.RuneCountInString(g.Str())
		if forward && offset > pos {
			return offset
		}
		if !forward && offset >= pos {
			return prev
		}
		prev = offset
	}
	return offset
}
func (b *Buffer) horizontal(forward bool) {
	start, end := b.lineBounds()
	if forward {
		if b.Cursor == end {
			if end < len(b.Text) {
				b.Cursor++
			}
		} else {
			b.Cursor = start + boundary(string(b.Text[start:end]), b.Cursor-start, true)
		}
	} else {
		if b.Cursor == start {
			if start > 0 {
				b.Cursor--
			}
		} else {
			b.Cursor = start + boundary(string(b.Text[start:end]), b.Cursor-start, false)
		}
	}
	b.goal = -1
}
func (b *Buffer) backspace() {
	end := b.Cursor
	b.horizontal(false)
	start := b.Cursor
	b.Cursor = end
	b.edit(start, end, "")
}
func (b *Buffer) deleteForward() {
	start := b.Cursor
	b.horizontal(true)
	end := b.Cursor
	b.Cursor = start
	b.edit(start, end, "")
}
func cellWidth(s string, col int) int {
	if s == "\t" {
		return tabWidth - col%tabWidth
	}
	if len(s) == 1 && (s[0] < 32 || s[0] == 127) {
		return 2
	}
	return uniseg.StringWidth(s)
}
func displayWidth(s string) int {
	col := 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		col += cellWidth(g.Str(), col)
	}
	return col
}
func runeAtCell(s string, target int) int {
	col, offset := 0, 0
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		width := cellWidth(g.Str(), col)
		if col+width > target {
			break
		}
		col += width
		offset += utf8.RuneCountInString(g.Str())
	}
	return offset
}
func (b *Buffer) vertical(delta int) {
	row, _ := b.rowCol()
	start, _ := b.lineBounds()
	if b.goal < 0 {
		b.goal = displayWidth(string(b.Text[start:b.Cursor]))
	}
	lines := b.lines()
	target := max(0, min(row+delta, len(lines)-1))
	offset := 0
	for i := 0; i < target; i++ {
		offset += utf8.RuneCountInString(lines[i]) + 1
	}
	b.Cursor = offset + runeAtCell(lines[target], b.goal)
}
func (b *Buffer) goTo(row, col int) bool {
	lines := b.lines()
	if row < 1 || row > len(lines) || col < 1 || col > utf8.RuneCountInString(lines[row-1])+1 {
		return false
	}
	offset := 0
	for i := 0; i < row-1; i++ {
		offset += utf8.RuneCountInString(lines[i]) + 1
	}
	b.Cursor = offset + col - 1
	b.goal = -1
	return true
}
func (b *Buffer) cutLine() string {
	start, end := b.lineBounds()
	if end < len(b.Text) {
		end++
	}
	s := string(b.Text[start:end])
	b.edit(start, end, "")
	return s
}
func (b *Buffer) find(query string) bool {
	if query == "" {
		return false
	}
	// Search after the cursor, wrapping once. Convert byte indexes back to rune offsets.
	start := min(b.Cursor+1, len(b.Text))
	suffix := string(b.Text[start:])
	if i := strings.Index(suffix, query); i >= 0 {
		b.Cursor = start + utf8.RuneCountInString(suffix[:i])
		b.goal = -1
		return true
	}
	whole := string(b.Text)
	if i := strings.Index(whole, query); i >= 0 {
		b.Cursor = utf8.RuneCountInString(whole[:i])
		b.goal = -1
		return true
	}
	return false
}
func (b *Buffer) replaceAll(query, replacement string) int {
	if query == "" {
		return 0
	}
	s := string(b.Text)
	count := strings.Count(s, query)
	if count > 0 {
		b.edit(0, len(b.Text), strings.ReplaceAll(s, query, replacement))
	}
	return count
}
