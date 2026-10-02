package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
)

const explorerPreviewBytes = 8192

type explorerEntry struct {
	Name  string
	Dir   bool
	Link  bool
	Size  int64
	Error bool
}

// explorer is a yazi-style three-column browser: parent | current | preview.
type explorer struct {
	dir        string
	entries    []explorerEntry
	index      int
	showHidden bool
	// remembered selection per directory so that going up restores the position
	positions map[string]string
}

func readExplorerDir(dir string, hidden bool) ([]explorerEntry, error) {
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	entries := make([]explorerEntry, 0, len(list))
	for _, d := range list {
		name := d.Name()
		if !hidden && strings.HasPrefix(name, ".") {
			continue
		}
		en := explorerEntry{Name: name, Link: d.Type()&os.ModeSymlink != 0}
		// Stat follows symlinks so a link to a directory is browsable.
		if info, err := os.Stat(filepath.Join(dir, name)); err == nil {
			en.Dir = info.IsDir()
			en.Size = info.Size()
		} else {
			en.Error = true
		}
		entries = append(entries, en)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func newExplorer(dir string) (*explorer, error) {
	x := &explorer{positions: map[string]string{}}
	return x, x.enter(dir, "")
}

// enter lists dir and selects the entry called focus when present.
func (x *explorer) enter(dir, focus string) error {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	entries, err := readExplorerDir(abs, x.showHidden)
	if err != nil {
		return err
	}
	x.dir, x.entries, x.index = abs, entries, 0
	if focus == "" {
		focus = x.positions[abs]
	}
	for i, en := range entries {
		if en.Name == focus {
			x.index = i
		}
	}
	return nil
}
func (x *explorer) selected() (explorerEntry, bool) {
	if x.index < 0 || x.index >= len(x.entries) {
		return explorerEntry{}, false
	}
	return x.entries[x.index], true
}
func (x *explorer) selectedPath() string {
	if en, ok := x.selected(); ok {
		return filepath.Join(x.dir, en.Name)
	}
	return ""
}
func (x *explorer) move(delta int) {
	x.index = max(0, min(len(x.entries)-1, x.index+delta))
}
func (x *explorer) remember() {
	if en, ok := x.selected(); ok {
		x.positions[x.dir] = en.Name
	}
}
func (x *explorer) up() error {
	parent := filepath.Dir(x.dir)
	if parent == x.dir {
		return nil
	}
	x.remember()
	return x.enter(parent, filepath.Base(x.dir))
}
func (x *explorer) toggleHidden() error {
	focus := ""
	if en, ok := x.selected(); ok {
		focus = en.Name
	}
	x.showHidden = !x.showHidden
	return x.enter(x.dir, focus)
}

// preview returns display lines for the selected entry: a listing for
// directories, leading text for text files, or a short note otherwise.
func (x *explorer) preview(limit int) []string {
	en, ok := x.selected()
	if !ok {
		return nil
	}
	path := filepath.Join(x.dir, en.Name)
	if en.Error {
		return []string{"(unreadable)"}
	}
	if en.Dir {
		list, err := readExplorerDir(path, x.showHidden)
		if err != nil {
			return []string{"(" + err.Error() + ")"}
		}
		if len(list) == 0 {
			return []string{"(empty)"}
		}
		var lines []string
		for _, c := range list {
			name := c.Name
			if c.Dir {
				name += "/"
			}
			lines = append(lines, name)
			if len(lines) >= limit {
				break
			}
		}
		return lines
	}
	f, err := os.Open(path)
	if err != nil {
		return []string{"(" + err.Error() + ")"}
	}
	defer f.Close()
	buf := make([]byte, explorerPreviewBytes)
	n, _ := f.Read(buf)
	buf = buf[:n]
	if bytes.IndexByte(buf, 0) >= 0 {
		return []string{"(binary file)"}
	}
	// A multibyte rune cut at the buffer end is not invalid content.
	if n == explorerPreviewBytes {
		for i := 0; i < utf8.UTFMax && len(buf) > 0 && !utf8.Valid(buf); i++ {
			buf = buf[:len(buf)-1]
		}
	}
	if !utf8.Valid(buf) {
		return []string{"(not UTF-8 text)"}
	}
	text := strings.ReplaceAll(string(buf), "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return lines
}

func (e *Editor) openExplorer() {
	dir, _ := os.Getwd()
	focus := ""
	if len(e.buffers) > 0 {
		if b := e.current(); b.Path != "" {
			dir, focus = filepath.Dir(b.Path), filepath.Base(b.Path)
		}
	}
	if e.explorerDir != "" {
		if info, err := os.Stat(e.explorerDir); err == nil && info.IsDir() && focus == "" {
			dir = e.explorerDir
		}
	}
	x, err := newExplorer(dir)
	if err != nil {
		e.fail(err)
		return
	}
	for i, en := range x.entries {
		if en.Name == focus {
			x.index = i
		}
	}
	e.explorer = x
	e.message = "Explorer: h/j/k/l move, Enter opens, . hidden, ~ path, q closes"
}
func (e *Editor) closeExplorer() {
	if e.explorer != nil {
		e.explorerDir = e.explorer.dir
	}
	e.explorer = nil
}
func (e *Editor) explorerKeys(ev *tcell.EventKey) {
	x := e.explorer
	_, h := e.screen.Size()
	page := max(1, h-5)
	key := ev.Key()
	r := rune(0)
	if key == tcell.KeyRune && ev.Modifiers()&(tcell.ModCtrl|tcell.ModAlt) == 0 {
		r = ev.Rune()
	}
	var err error
	switch {
	case key == tcell.KeyEscape || key == tcell.KeyCtrlC || r == 'q':
		e.closeExplorer()
		e.message = "Explorer closed"
	case key == tcell.KeyUp || r == 'k':
		x.move(-1)
	case key == tcell.KeyDown || r == 'j':
		x.move(1)
	case key == tcell.KeyPgUp || key == tcell.KeyCtrlB:
		x.move(-page)
	case key == tcell.KeyPgDn || key == tcell.KeyCtrlF:
		x.move(page)
	case key == tcell.KeyHome || r == 'g':
		x.index = 0
	case key == tcell.KeyEnd || r == 'G':
		x.index = max(0, len(x.entries)-1)
	case key == tcell.KeyLeft || r == 'h' || key == tcell.KeyBackspace || key == tcell.KeyBackspace2:
		err = x.up()
	case key == tcell.KeyRight || key == tcell.KeyEnter || r == 'l':
		en, ok := x.selected()
		if !ok {
			return
		}
		path := x.selectedPath()
		if en.Dir {
			x.remember()
			err = x.enter(path, "")
		} else if key != tcell.KeyRight && r != 'l' || !en.Error {
			if openErr := e.open(path); openErr != nil {
				e.fail(openErr)
				return
			}
			e.closeExplorer()
		}
	case r == '.':
		err = x.toggleHidden()
	case r == '~' || r == '/' || r == ':':
		dir := x.dir + string(filepath.Separator)
		e.explorer = nil
		e.explorerDir = x.dir
		e.ask("Open file:", dir, func(path string) {
			if err := e.open(path); err != nil {
				e.fail(err)
			}
		})
	}
	if err != nil {
		e.fail(err)
	}
}
func (e *Editor) drawExplorer(w, h int) {
	x := e.explorer
	s := e.screen
	s.Clear()
	s.HideCursor()
	e.bar(0, w, barStyle)
	e.text(0, 0, w, " Open | "+x.dir, barStyle)
	listH := max(1, h-4)
	// Column layout: parent 1/5, current 2/5, preview 2/5 (collapsing on narrow terminals).
	parentW, curW := w/5, w*2/5
	if w < 60 {
		parentW = 0
		curW = w / 2
	}
	prevX := parentW + curW
	if parentW > 0 {
		parent := filepath.Dir(x.dir)
		if parent != x.dir {
			if list, err := readExplorerDir(parent, x.showHidden); err == nil {
				base := filepath.Base(x.dir)
				sel := 0
				for i, en := range list {
					if en.Name == base {
						sel = i
					}
				}
				e.drawExplorerList(0, 1, parentW-1, listH, list, sel, false)
			}
		}
	}
	e.drawExplorerList(parentW, 1, curW-1, listH, x.entries, x.index, true)
	if prevX < w {
		for y := 1; y <= listH; y++ {
			s.SetContent(prevX-1, y, '│', nil, dimStyle)
		}
		for i, line := range x.preview(listH) {
			e.text(prevX, i+1, w-prevX, line, dimStyle)
		}
	}
	e.bar(h-3, w, barStyle)
	status := " empty directory"
	if en, ok := x.selected(); ok {
		status = fmt.Sprintf(" %d/%d  %s", x.index+1, len(x.entries), en.Name)
		if !en.Dir {
			status += fmt.Sprintf("  %d bytes", en.Size)
		}
	}
	if x.showHidden {
		status += "  [hidden shown]"
	}
	e.text(0, h-3, w, status, barStyle)
	e.text(0, h-2, w, "h/← Up  j/k/↑/↓ Move  l/→/Enter Open  g/G Top/Bottom  . Hidden", messageStyle)
	e.text(0, h-1, w, "~ Type path  q/Esc Close", messageStyle)
}
func (e *Editor) drawExplorerList(x, y, width, height int, list []explorerEntry, sel int, focus bool) {
	if width <= 0 {
		return
	}
	top := max(0, sel-height+1)
	for i := top; i < len(list) && i-top < height; i++ {
		en := list[i]
		name := en.Name
		style := normalStyle
		if en.Dir {
			name += "/"
			style = normalStyle.Foreground(tcell.ColorAqua).Bold(true)
		}
		if i == sel {
			if focus {
				style = activeStyle
			} else {
				style = barStyle
			}
			for cx := 0; cx < width; cx++ {
				e.screen.SetContent(x+cx, y+i-top, ' ', nil, style)
			}
		}
		e.text(x, y+i-top, width, " "+name, style)
	}
}
