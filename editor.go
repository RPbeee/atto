package main

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
)

type prompt struct {
	label   string
	text    []rune
	cursor  int
	confirm bool
	submit  func(string)
}

type Editor struct {
	screen                    tcell.Screen
	buffers                   []*Buffer
	active                    int
	message                   string
	prompt                    *prompt
	clipboard, query          string
	done, help, listing       bool
	listIndex                 int
	pasting                   bool
	paste                     strings.Builder
	mark                      int
	panes                     []Pane
	focused                   int
	horizontalSplit           bool
	paintActive               bool
	paintRow                  int
	rect                      *rectangle
	rectClipboard             []string
	projectRoot, projectQuery string
	projectView               bool
	projectIndex              int
	projectResults            projectReport
	projectJob                *projectJob
	projectWorkers            projectWorkers
	syntaxEnabled             bool
	paintSyntax               *syntaxCache
	explorer                  *explorer
	explorerDir               string
}

func newEditor(screen tcell.Screen) *Editor {
	return &Editor{screen: screen, mark: -1, syntaxEnabled: true}
}
func (e *Editor) current() *Buffer { return e.buffers[e.active] }
func (e *Editor) open(path string) error {
	canonicalPath, err := canonical(path)
	if err != nil {
		return err
	}
	for i, b := range e.buffers {
		if b.Path == canonicalPath {
			e.switchTo(i)
			e.message = "Already open"
			return nil
		}
	}
	b, err := loadBuffer(canonicalPath)
	if err != nil {
		return err
	}
	e.buffers = append(e.buffers, b)
	e.switchTo(len(e.buffers) - 1)
	e.message = "Opened " + b.name()
	return nil
}
func (e *Editor) switchTo(i int) {
	e.savePane()
	e.active = i
	e.clearSelection()
	if len(e.panes) > 0 {
		e.setPaneBuffer(e.current())
	}
}
func (e *Editor) next(delta int) { e.switchTo((e.active + delta + len(e.buffers)) % len(e.buffers)) }
func (e *Editor) ask(label, initial string, submit func(string)) {
	e.prompt = &prompt{label: label, text: []rune(initial), cursor: len([]rune(initial)), submit: submit}
}
func (e *Editor) confirm(label string, submit func(string)) {
	e.prompt = &prompt{label: label + " [y/n, Esc cancels]", confirm: true, submit: submit}
}
func (e *Editor) fail(err error) { e.message = "Error: " + err.Error() }
func (e *Editor) saveBuffer(b *Buffer, askName bool, then func()) {
	saveTo := func(path string) {
		if path == "" {
			e.message = "Save cancelled"
			return
		}
		target, err := canonical(path)
		if err != nil {
			e.fail(err)
			return
		}
		for _, other := range e.buffers {
			if other != b && other.Path == target {
				e.message = "Error: target is open in another buffer"
				return
			}
		}
		expected := b.disk
		if target != b.Path {
			_, expected, _, err = readDisk(target)
			if err != nil {
				e.fail(err)
				return
			}
		}
		write := func() {
			if err := b.save(target, expected); err != nil {
				e.fail(err)
				return
			}
			e.message = "Saved " + b.name()
			if then != nil {
				then()
			}
		}
		if target != b.Path && expected.exists {
			e.confirm("Overwrite "+target+"?", func(answer string) {
				if answer == "y" {
					write()
				} else {
					e.message = "Save cancelled"
				}
			})
			return
		}
		write()
	}
	if askName || b.Path == "" {
		e.ask("Write file:", b.Path, saveTo)
	} else {
		saveTo(b.Path)
	}
}
func (e *Editor) saveAll(index int) {
	for index < len(e.buffers) && !e.buffers[index].dirty() {
		index++
	}
	if index == len(e.buffers) {
		e.message = "All modified buffers saved"
		return
	}
	e.switchTo(index)
	e.saveBuffer(e.current(), false, func() { e.saveAll(index + 1) })
}
func (e *Editor) closeCurrent() {
	b := e.current()
	close := func() {
		e.buffers = append(e.buffers[:e.active], e.buffers[e.active+1:]...)
		if len(e.buffers) == 0 {
			e.panes = nil
			e.done = true
			return
		}
		e.active = min(e.active, len(e.buffers)-1)
		if len(e.panes) > 0 {
			for i := range e.panes {
				delete(e.panes[i].views, b)
				if e.panes[i].Buffer == b {
					e.panes[i] = paneOf(e.current())
				}
			}
			e.restorePane()
		} else {
			e.clearSelection()
		}
		e.message = "Buffer closed"
	}
	if !b.dirty() {
		close()
		return
	}
	e.confirm("Save changes to "+b.name()+" before closing?", func(answer string) {
		if answer == "y" {
			e.saveBuffer(b, false, close)
		} else {
			close()
		}
	})
}
func (e *Editor) quit() {
	// Keep every buffer open if the user cancels any step in the exit sequence.
	skipped := map[*Buffer]bool{}
	var check func()
	check = func() {
		for i, b := range e.buffers {
			if b.dirty() && !skipped[b] {
				e.switchTo(i)
				e.confirm("Save changes to "+b.name()+" before quitting?", func(answer string) {
					if answer == "y" {
						e.saveBuffer(b, false, check)
					} else {
						skipped[b] = true
						check()
					}
				})
				return
			}
		}
		e.done = true
	}
	check()
}
func (e *Editor) selection() (int, int, bool) {
	if e.rect != nil || e.mark < 0 || e.mark == e.current().Cursor {
		return 0, 0, false
	}
	return min(e.mark, e.current().Cursor), max(e.mark, e.current().Cursor), true
}
func (e *Editor) insert(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.ReplaceAll(s, "\x00", "")
	b := e.current()
	if e.rect != nil {
		e.rectangleInsert(s)
		return
	}
	if start, end, ok := e.selection(); ok {
		b.edit(start, end, s)
	} else {
		b.insert(s)
	}
	e.clearSelection()
}
func (e *Editor) cut(copyOnly bool) {
	if e.rect != nil {
		e.rectangleCut(copyOnly)
		return
	}
	e.rectClipboard = nil
	b := e.current()
	if start, end, ok := e.selection(); ok {
		e.clipboard = string(b.Text[start:end])
		if !copyOnly {
			b.edit(start, end, "")
		}
	} else {
		if copyOnly {
			start, end := b.lineBounds()
			if end < len(b.Text) {
				end++
			}
			e.clipboard = string(b.Text[start:end])
		} else {
			e.clipboard = b.cutLine()
		}
	}
	e.clearSelection()
	e.message = "Copied to internal clipboard"
	if !copyOnly {
		e.message = "Cut to internal clipboard"
	}
}
func (e *Editor) search() { e.ask("Search:", e.query, func(q string) { e.query = q; e.findNext() }) }
func (e *Editor) findNext() {
	if e.query == "" {
		e.message = "Use Ctrl-W to enter a search"
		return
	}
	e.clearSelection()
	if e.current().find(e.query) {
		e.message = "Found: " + e.query
	} else {
		e.message = "Not found: " + e.query
	}
}
func (e *Editor) replace() {
	e.ask("Replace text:", e.query, func(q string) {
		if q == "" {
			e.message = "Empty search cancelled"
			return
		}
		e.query = q
		e.ask("Replace with:", "", func(replacement string) {
			e.confirm("Replace all occurrences in this buffer?", func(answer string) {
				if answer == "y" {
					n := e.current().replaceAll(q, replacement)
					e.clearSelection()
					e.message = fmt.Sprintf("Replaced %d occurrences", n)
				} else {
					e.message = "Replace cancelled"
				}
			})
		})
	})
}
func (e *Editor) handlePrompt(ev *tcell.EventKey) {
	p := e.prompt
	if ev.Key() == tcell.KeyEscape || ev.Key() == tcell.KeyCtrlC {
		e.prompt = nil
		e.message = "Cancelled"
		return
	}
	if p.confirm {
		if ev.Key() == tcell.KeyRune {
			s := strings.ToLower(string(ev.Rune()))
			if s == "y" || s == "n" {
				e.prompt = nil
				p.submit(s)
			}
		}
		return
	}
	switch ev.Key() {
	case tcell.KeyEnter:
		e.prompt = nil
		p.submit(string(p.text))
	case tcell.KeyLeft:
		p.cursor = max(0, p.cursor-1)
	case tcell.KeyRight:
		p.cursor = min(len(p.text), p.cursor+1)
	case tcell.KeyHome, tcell.KeyCtrlA:
		p.cursor = 0
	case tcell.KeyEnd, tcell.KeyCtrlE:
		p.cursor = len(p.text)
	case tcell.KeyCtrlU:
		p.text = nil
		p.cursor = 0
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if p.cursor > 0 {
			p.text = append(p.text[:p.cursor-1], p.text[p.cursor:]...)
			p.cursor--
		}
	case tcell.KeyDelete:
		if p.cursor < len(p.text) {
			p.text = append(p.text[:p.cursor], p.text[p.cursor+1:]...)
		}
	case tcell.KeyRune:
		if ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == 0 {
			p.text = append(p.text[:p.cursor], append([]rune{ev.Rune()}, p.text[p.cursor:]...)...)
			p.cursor++
		}
	}
}
func (e *Editor) handle(ev tcell.Event) {
	e.pollProject()
	if len(e.buffers) > 0 {
		b := e.current()
		old := b.Text
		defer func() { e.reconcileViews(b, old) }()
	}
	switch event := ev.(type) {
	case *tcell.EventResize:
		e.screen.Sync()
	case *tcell.EventPaste:
		if event.Start() {
			e.pasting = true
			e.paste.Reset()
		} else {
			s := e.paste.String()
			e.pasting = false
			e.paste.Reset()
			if e.prompt != nil {
				if !e.prompt.confirm {
					p := e.prompt
					s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "")
					runes := []rune(s)
					p.text = append(p.text[:p.cursor], append(runes, p.text[p.cursor:]...)...)
					p.cursor += len(runes)
				}
			} else if !e.help && !e.listing && !e.projectView && e.explorer == nil {
				e.insert(s)
			}
		}
	case *tcell.EventKey:
		e.handleKey(event)
	}
}
func (e *Editor) handleKey(ev *tcell.EventKey) {
	if e.pasting {
		switch ev.Key() {
		case tcell.KeyRune:
			e.paste.WriteRune(ev.Rune())
		case tcell.KeyEnter:
			e.paste.WriteByte('\n')
		case tcell.KeyTab:
			e.paste.WriteByte('\t')
		}
		return
	}
	if e.prompt != nil {
		e.handlePrompt(ev)
		return
	}
	if e.projectView {
		e.projectKeys(ev)
		return
	}
	if e.explorer != nil {
		e.explorerKeys(ev)
		return
	}
	if e.help {
		e.help = false
		return
	}
	if e.listing {
		switch ev.Key() {
		case tcell.KeyEscape, tcell.KeyCtrlC:
			e.listing = false
		case tcell.KeyUp:
			e.listIndex = max(0, e.listIndex-1)
		case tcell.KeyDown:
			e.listIndex = min(len(e.buffers)-1, e.listIndex+1)
		case tcell.KeyEnter:
			e.switchTo(e.listIndex)
			e.listing = false
		}
		return
	}
	b := e.current()
	if ev.Modifiers()&tcell.ModAlt != 0 && ev.Key() == tcell.KeyRune {
		switch ev.Rune() {
		case ']':
			e.next(1)
		case '[':
			e.next(-1)
		case 'u', 'U':
			b.undoEdit()
			e.clearSelection()
		case 'e', 'E':
			b.redoEdit()
			e.clearSelection()
		case 'w', 'W':
			e.findNext()
		case '6':
			e.cut(true)
		case 'l', 'L':
			e.chooseSyntax()
		case 'h', 'H':
			e.syntaxEnabled = !e.syntaxEnabled
			e.message = "Syntax highlighting toggled (F9)"
		case 'r', 'R':
			e.toggleRectangle()
		case 'o', 'O':
			e.focusOther()
		case 'p', 'P':
			e.projectView = true
			e.message = fmt.Sprintf("Project: %d matches", len(e.projectResults.Matches))
		}
		return
	}
	switch ev.Key() {
	case tcell.KeyF9:
		e.syntaxEnabled = !e.syntaxEnabled
		e.message = "Syntax highlighting toggled (F9)"
	case tcell.KeyF3:
		e.split(false)
	case tcell.KeyF4:
		e.split(true)
	case tcell.KeyF7:
		e.focusOther()
	case tcell.KeyF8:
		e.closePane()
	case tcell.KeyCtrlP:
		e.askProject()
	case tcell.KeyCtrlG, tcell.KeyF1:
		e.help = true
	case tcell.KeyCtrlN:
		e.buffers = append(e.buffers, newBuffer())
		e.switchTo(len(e.buffers) - 1)
		e.message = "New buffer"
	case tcell.KeyCtrlR:
		e.openExplorer()
	case tcell.KeyCtrlO:
		e.saveBuffer(b, true, nil)
	case tcell.KeyCtrlS:
		e.saveBuffer(b, false, nil)
	case tcell.KeyF2:
		e.saveAll(0)
	case tcell.KeyCtrlX:
		e.closeCurrent()
	case tcell.KeyCtrlQ:
		e.quit()
	case tcell.KeyF5:
		e.next(-1)
	case tcell.KeyF6:
		e.next(1)
	case tcell.KeyCtrlB:
		e.listing = true
		e.listIndex = e.active
	case tcell.KeyCtrlW:
		e.search()
	case tcell.KeyCtrlBackslash:
		e.replace()
	case tcell.KeyCtrlJ:
		e.ask("Go to line[:column]:", "", func(s string) {
			parts := strings.Split(s, ":")
			row, err := strconv.Atoi(parts[0])
			col := 1
			if len(parts) == 2 {
				var columnErr error
				col, columnErr = strconv.Atoi(parts[1])
				if columnErr != nil {
					err = columnErr
				}
			}
			if err != nil || len(parts) > 2 || !b.goTo(row, col) {
				e.message = "Invalid line or column"
			} else {
				e.clearSelection()
			}
		})
	case tcell.KeyCtrlK:
		e.cut(false)
	case tcell.KeyCtrlU:
		e.pasteClipboard()
	case tcell.KeyCtrlSpace, tcell.KeyCtrlCarat:
		if e.rect != nil {
			e.clearSelection()
		}
		if e.mark < 0 {
			e.mark = b.Cursor
			e.message = "Selection started"
		} else {
			e.clearSelection()
			e.message = "Selection cleared"
		}
	case tcell.KeyEscape:
		e.clearSelection()
		e.message = "Selection cleared"
	case tcell.KeyLeft, tcell.KeyRight, tcell.KeyUp, tcell.KeyDown, tcell.KeyHome, tcell.KeyEnd, tcell.KeyPgUp, tcell.KeyPgDn, tcell.KeyCtrlA, tcell.KeyCtrlE:
		if e.rect != nil {
			e.rectangleMove(ev.Key())
			return
		}
		if ev.Modifiers()&tcell.ModShift != 0 && e.mark < 0 {
			e.mark = b.Cursor
		}
		switch ev.Key() {
		case tcell.KeyLeft:
			b.horizontal(false)
		case tcell.KeyRight:
			b.horizontal(true)
		case tcell.KeyUp:
			b.vertical(-1)
		case tcell.KeyDown:
			b.vertical(1)
		case tcell.KeyPgUp:
			b.vertical(-e.pageHeight())
		case tcell.KeyPgDn:
			b.vertical(e.pageHeight())
		case tcell.KeyHome, tcell.KeyCtrlA:
			b.Cursor, _ = b.lineBounds()
			b.goal = -1
		case tcell.KeyEnd, tcell.KeyCtrlE:
			_, b.Cursor = b.lineBounds()
			b.goal = -1
		}
	case tcell.KeyBackspace, tcell.KeyBackspace2:
		if e.rect != nil {
			e.rectangleDelete(true)
			return
		}
		if start, end, ok := e.selection(); ok {
			b.edit(start, end, "")
		} else {
			b.backspace()
		}
		e.clearSelection()
	case tcell.KeyDelete:
		if e.rect != nil {
			e.rectangleDelete(false)
			return
		}
		if start, end, ok := e.selection(); ok {
			b.edit(start, end, "")
		} else {
			b.deleteForward()
		}
		e.clearSelection()
	case tcell.KeyEnter:
		if e.rect != nil {
			e.message = "Esc finishes rectangular editing before inserting a newline"
			return
		}
		e.insert("\n")
	case tcell.KeyTab:
		e.insert("\t")
	case tcell.KeyRune:
		if ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == 0 {
			e.insert(string(ev.Rune()))
		}
	}
}
func shortName(b *Buffer) string {
	if b.Path == "" {
		return "[untitled]"
	}
	return filepath.Base(b.Path)
}
