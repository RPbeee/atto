package main

import (
	"reflect"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func TestRectangleTypingAndDeleteAcrossShortLines(t *testing.T) {
	e := testEditor(t)
	typed(e, "abc\nx\n123")
	e.current().goTo(1, 3)
	alt(e, 'r')
	key(e, tcell.KeyDown)
	key(e, tcell.KeyDown)
	typed(e, "XY")
	if string(e.current().Text) != "abXYc\nx XY\n12XY3" {
		t.Fatal(string(e.current().Text))
	}
	key(e, tcell.KeyBackspace2)
	if string(e.current().Text) != "abXc\nx X\n12X3" {
		t.Fatal("rectangular backspace", string(e.current().Text))
	}
	alt(e, 'u')
	if string(e.current().Text) != "abXYc\nx XY\n12XY3" || e.rect != nil {
		t.Fatal("rectangle undo")
	}
	key(e, tcell.KeyEscape)
	typed(e, "!")
	if string(e.current().Text) != "abXYc\nx XY\n12XY!3" {
		t.Fatal("normal typing after rectangle")
	}
}
func TestRectangleCutPasteAndSingleUndo(t *testing.T) {
	e := testEditor(t)
	typed(e, "abc\nDEF\nxyz")
	e.current().goTo(1, 2)
	alt(e, 'r')
	key(e, tcell.KeyRight)
	key(e, tcell.KeyDown)
	key(e, tcell.KeyCtrlK)
	if string(e.current().Text) != "ac\nDF\nxyz" || !reflect.DeepEqual(e.rectClipboard, []string{"b", "E"}) {
		t.Fatal(string(e.current().Text), e.rectClipboard)
	}
	alt(e, 'u')
	if string(e.current().Text) != "abc\nDEF\nxyz" {
		t.Fatal("cut undo")
	}
	e.current().goTo(3, 4)
	key(e, tcell.KeyCtrlU)
	if string(e.current().Text) != "abc\nDEF\nxyzb\n   E" {
		t.Fatal("rectangle clipboard paste", string(e.current().Text))
	}
	alt(e, 'u')
	if string(e.current().Text) != "abc\nDEF\nxyz" {
		t.Fatal("paste undo")
	}
}
func TestRectangleCopyTabsWideAndCombining(t *testing.T) {
	b := bufferWith("\t日e\u0301\nx\n👩‍💻z")
	if got := b.copyRectangle(0, 2, 1, 5); !reflect.DeepEqual(got, []string{"    ", "    ", " z  "}) {
		t.Fatal(got)
	}
	b.replaceRectangle(0, 0, 5, 6, []string{"X"})
	if string(b.Text) != "     Xe\u0301\nx\n👩‍💻z" {
		t.Fatal("partial wide glyph must preserve geometry with spaces", string(b.Text))
	}
	b.undoEdit()
	if string(b.Text) != "\t日e\u0301\nx\n👩‍💻z" {
		t.Fatal("unicode undo")
	}
	b = bufferWith("a\nb")
	b.replaceRectangle(0, 1, 1, 1, []string{"\t"})
	if string(b.Text) != "a   \nb   " {
		t.Fatal("inserted tab width", string(b.Text))
	}
}
func TestRectangleDeleteBeyondEOLDoesNotPad(t *testing.T) {
	b := bufferWith("a\n")
	b.replaceRectangle(0, 1, 4, 6, []string{""})
	if string(b.Text) != "a\n" || b.dirty() {
		t.Fatal("deletion added spaces", string(b.Text))
	}
}
func TestRectangleNoOpDoesNotRewriteTabs(t *testing.T) {
	b := bufferWith("\tx\n\ty")
	b.replaceRectangle(0, 1, 8, 9, []string{""})
	if string(b.Text) != "\tx\n\ty" || b.dirty() || len(b.undo) != 0 {
		t.Fatal("empty deletion rewrote unrelated tabs", string(b.Text))
	}
}
func TestRectangleMultilinePasteAndHighlight(t *testing.T) {
	e := testEditor(t)
	typed(e, "abc\nx\n123")
	e.current().goTo(1, 2)
	alt(e, 'r')
	key(e, tcell.KeyRight)
	key(e, tcell.KeyDown)
	e.draw()
	screen := e.screen.(tcell.SimulationScreen)
	_, _, style, _ := screen.GetContent(4, 3)
	if style != selectionStyle {
		t.Fatal("virtual rectangle not highlighted")
	}
	e.handle(tcell.NewEventPaste(true))
	typed(e, "Y\nZ")
	e.handle(tcell.NewEventPaste(false))
	if string(e.current().Text) != "aYc\nxZ\n123" {
		t.Fatal(string(e.current().Text))
	}
	alt(e, 'u')
	if string(e.current().Text) != "abc\nx\n123" {
		t.Fatal("multiline paste undo")
	}
}
