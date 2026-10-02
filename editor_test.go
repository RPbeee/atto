package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func testEditor(t *testing.T) *Editor {
	t.Helper()
	screen := tcell.NewSimulationScreen("UTF-8")
	if err := screen.Init(); err != nil {
		t.Fatal(err)
	}
	screen.SetSize(80, 24)
	t.Cleanup(screen.Fini)
	e := newEditor(screen)
	t.Cleanup(e.shutdownProject)
	e.buffers = append(e.buffers, newBuffer())
	return e
}
func key(e *Editor, k tcell.Key) { e.handle(tcell.NewEventKey(k, 0, tcell.ModNone)) }
func typed(e *Editor, s string) {
	for _, r := range s {
		if r == '\n' {
			key(e, tcell.KeyEnter)
		} else if r == '\t' {
			key(e, tcell.KeyTab)
		} else {
			e.handle(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
		}
	}
}
func alt(e *Editor, r rune) { e.handle(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModAlt)) }
func answer(e *Editor, s string) {
	p := e.prompt
	typed(e, s)
	if p != nil && !p.confirm && e.prompt == p {
		key(e, tcell.KeyEnter)
	}
}
func fileText(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
func TestMultipleBuffersIndependentState(t *testing.T) {
	e := testEditor(t)
	typed(e, "日本語\nfirst")
	first := e.current()
	first.Top = 3
	first.Left = 2
	cursor := first.Cursor
	key(e, tcell.KeyCtrlN)
	typed(e, "second")
	second := e.current()
	key(e, tcell.KeyF5)
	if e.current() != first || first.Cursor != cursor || first.Top != 3 || first.Left != 2 {
		t.Fatal("buffer state lost")
	}
	alt(e, 'u')
	if string(first.Text) != "日本語\nfirs" || string(second.Text) != "second" {
		t.Fatal("undo crossed buffers")
	}
	key(e, tcell.KeyF6)
	alt(e, 'u')
	if string(second.Text) != "secon" {
		t.Fatal("second undo")
	}
	alt(e, 'e')
	if string(second.Text) != "second" {
		t.Fatal("second redo")
	}
}
func TestDuplicateOpenAndSaveAsGuard(t *testing.T) {
	e := testEditor(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "one")
	writeFixture(t, path, []byte("one"))
	other := filepath.Join(dir, "two")
	writeFixture(t, other, []byte("two"))
	if err := e.open(path); err != nil {
		t.Fatal(err)
	}
	n := len(e.buffers)
	if err := e.open(filepath.Join(dir, ".", "one")); err != nil || len(e.buffers) != n {
		t.Fatal("duplicate open")
	}
	if err := e.open(other); err != nil {
		t.Fatal(err)
	}
	typed(e, "edit")
	e.saveBuffer(e.current(), true, nil)
	key(e, tcell.KeyCtrlU)
	answer(e, path)
	if !strings.Contains(e.message, "another buffer") || fileText(t, path) != "one" {
		t.Fatal("open save target overwritten", e.message)
	}
}
func TestSaveAllIncludingUntitled(t *testing.T) {
	e := testEditor(t)
	dir := t.TempDir()
	typed(e, "first")
	key(e, tcell.KeyCtrlN)
	typed(e, "second")
	key(e, tcell.KeyF2)
	if e.prompt == nil {
		t.Fatal("missing first save prompt")
	}
	answer(e, filepath.Join(dir, "one"))
	if e.prompt == nil {
		t.Fatal("missing second save prompt")
	}
	answer(e, filepath.Join(dir, "two"))
	if fileText(t, filepath.Join(dir, "one")) != "first" || fileText(t, filepath.Join(dir, "two")) != "second" || e.buffers[0].dirty() || e.buffers[1].dirty() {
		t.Fatal("save all failed")
	}
}
func TestCloseAndQuitCancellation(t *testing.T) {
	e := testEditor(t)
	typed(e, "one")
	key(e, tcell.KeyCtrlN)
	typed(e, "two")
	key(e, tcell.KeyCtrlX)
	key(e, tcell.KeyEscape)
	if len(e.buffers) != 2 || e.done {
		t.Fatal("close cancellation")
	}
	key(e, tcell.KeyCtrlQ)
	answer(e, "n")
	key(e, tcell.KeyEscape)
	if e.done || len(e.buffers) != 2 || !e.buffers[0].dirty() || !e.buffers[1].dirty() {
		t.Fatal("quit cancellation lost buffers")
	}
	key(e, tcell.KeyCtrlX)
	answer(e, "n")
	if len(e.buffers) != 1 || e.done {
		t.Fatal("close did not affect only active buffer")
	}
	key(e, tcell.KeyCtrlQ)
	answer(e, "n")
	if !e.done {
		t.Fatal("quit discard")
	}
}
func TestQuitSaveAndSaveFailure(t *testing.T) {
	e := testEditor(t)
	typed(e, "work")
	key(e, tcell.KeyCtrlQ)
	answer(e, "y")
	answer(e, filepath.Join(t.TempDir(), "missing", "text"))
	if e.done || !e.current().dirty() || !strings.HasPrefix(e.message, "Error:") {
		t.Fatal("failed save exited editor")
	}
	path := filepath.Join(t.TempDir(), "saved")
	key(e, tcell.KeyCtrlQ)
	answer(e, "y")
	answer(e, path)
	if !e.done || fileText(t, path) != "work" {
		t.Fatal("save then quit failed")
	}
}
func TestSaveAsOverwriteConfirmationAndRace(t *testing.T) {
	e := testEditor(t)
	typed(e, "edited")
	path := filepath.Join(t.TempDir(), "exists")
	writeFixture(t, path, []byte("original"))
	e.saveBuffer(e.current(), true, nil)
	answer(e, path)
	if e.prompt == nil || !e.prompt.confirm {
		t.Fatal("no overwrite confirmation")
	}
	answer(e, "n")
	if fileText(t, path) != "original" {
		t.Fatal("overwrote on no")
	}
	e.saveBuffer(e.current(), true, nil)
	answer(e, path)
	writeFixture(t, path, []byte("external"))
	answer(e, "y")
	if fileText(t, path) != "external" || !e.current().dirty() {
		t.Fatal("confirmation race not checked")
	}
	e.saveBuffer(e.current(), true, nil)
	answer(e, path)
	answer(e, "y")
	if fileText(t, path) != "edited" || e.current().dirty() {
		t.Fatal("confirmed overwrite failed")
	}
}
func TestSelectionCutCopyPaste(t *testing.T) {
	e := testEditor(t)
	typed(e, "abc\ndef")
	key(e, tcell.KeyHome)
	key(e, tcell.KeyCtrlSpace)
	key(e, tcell.KeyRight)
	key(e, tcell.KeyRight)
	alt(e, '6')
	if e.clipboard != "de" || string(e.current().Text) != "abc\ndef" {
		t.Fatal("copy selection")
	}
	key(e, tcell.KeyCtrlU)
	if string(e.current().Text) != "abc\ndedef" {
		t.Fatal(string(e.current().Text))
	}
	alt(e, 'u')
	key(e, tcell.KeyHome)
	key(e, tcell.KeyCtrlSpace)
	key(e, tcell.KeyRight)
	key(e, tcell.KeyCtrlK)
	if e.clipboard != "d" || string(e.current().Text) != "abc\nef" {
		t.Fatal("cut selection")
	}
}
func TestBracketedPasteIsOneEditAndNoCommands(t *testing.T) {
	e := testEditor(t)
	e.handle(tcell.NewEventPaste(true))
	typed(e, "日本")
	key(e, tcell.KeyEnter)
	key(e, tcell.KeyTab)
	typed(e, "text")
	key(e, tcell.KeyCtrlQ)
	e.handle(tcell.NewEventPaste(false))
	if string(e.current().Text) != "日本\n\ttext" || e.done || e.prompt != nil {
		t.Fatal("paste executed command")
	}
	alt(e, 'u')
	if len(e.current().Text) != 0 {
		t.Fatal("paste not grouped")
	}
	key(e, tcell.KeyCtrlO)
	e.handle(tcell.NewEventPaste(true))
	typed(e, "a")
	key(e, tcell.KeyEnter)
	typed(e, "b")
	e.handle(tcell.NewEventPaste(false))
	if string(e.prompt.text) != "ab" {
		t.Fatal("prompt paste submitted prompt")
	}
}
func TestRenderResizeAndBufferList(t *testing.T) {
	e := testEditor(t)
	screen := e.screen.(tcell.SimulationScreen)
	typed(e, "日本\tword\ne\u0301\n👩‍💻")
	e.draw()
	r, _, _, _ := screen.GetContent(3, 2)
	if r != '日' {
		t.Fatalf("Japanese not rendered: %q", r)
	}
	for i := 0; i < 20; i++ {
		key(e, tcell.KeyCtrlN)
		typed(e, "x")
	}
	e.draw()
	key(e, tcell.KeyCtrlB)
	key(e, tcell.KeyUp)
	key(e, tcell.KeyEnter)
	if e.active != 19 {
		t.Fatal(e.active)
	}
	key(e, tcell.KeyCtrlG)
	e.draw()
	key(e, tcell.KeyEscape)
	screen.SetSize(24, 8)
	e.draw()
	screen.SetSize(10, 3)
	e.draw()
	screen.SetSize(80, 24)
	e.draw()
}
