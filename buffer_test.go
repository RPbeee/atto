package main

import (
	"strings"
	"testing"
)

func bufferWith(text string) *Buffer {
	b := newBuffer()
	b.Text = []rune(text)
	b.saved = text
	return b
}
func TestEditUndoAndSavedState(t *testing.T) {
	b := bufferWith("日本語\nsecond")
	b.Cursor = 3
	b.insert("！")
	if string(b.Text) != "日本語！\nsecond" || !b.dirty() {
		t.Fatal(b)
	}
	b.undoEdit()
	if b.dirty() || b.Cursor != 3 {
		t.Fatal("undo did not restore original state")
	}
	b.redoEdit()
	if !b.dirty() {
		t.Fatal("redo")
	}
	b.saved = string(b.Text)
	b.undoEdit()
	if !b.dirty() {
		t.Fatal("undo past save must be dirty")
	}
	b.redoEdit()
	if b.dirty() {
		t.Fatal("redo to save must be clean")
	}
	b.undoEdit()
	b.insert("?")
	b.redoEdit()
	if string(b.Text) != "日本語?\nsecond" {
		t.Fatal("new edit should invalidate redo")
	}
}
func TestGraphemeEditing(t *testing.T) {
	for _, cluster := range []string{"か\u3099", "👩‍💻", "🇯🇵", "e\u0301"} {
		t.Run(cluster, func(t *testing.T) {
			b := bufferWith(cluster + "x")
			b.horizontal(true)
			if b.Cursor != len([]rune(cluster)) {
				t.Fatal(b.Cursor)
			}
			b.backspace()
			if string(b.Text) != "x" {
				t.Fatal(string(b.Text))
			}
			b.undoEdit()
			b.Cursor = 0
			b.deleteForward()
			if string(b.Text) != "x" {
				t.Fatal(string(b.Text))
			}
		})
	}
}
func TestNewlineAndBoundaries(t *testing.T) {
	b := bufferWith("a\nb")
	b.Cursor = 2
	b.backspace()
	if string(b.Text) != "ab" {
		t.Fatal(string(b.Text))
	}
	b.undoEdit()
	b.Cursor = 1
	b.deleteForward()
	if string(b.Text) != "ab" {
		t.Fatal(string(b.Text))
	}
	b.Cursor = 0
	b.backspace()
	if string(b.Text) != "ab" {
		t.Fatal("start backspace")
	}
	b.Cursor = 2
	b.deleteForward()
	if string(b.Text) != "ab" {
		t.Fatal("end delete")
	}
}
func TestVerticalDisplayColumn(t *testing.T) {
	b := bufferWith("日\tx\na\n12345")
	b.Cursor = 2
	b.vertical(1)
	if b.Cursor != 5 {
		t.Fatal(b.Cursor)
	}
	b.vertical(1)
	row, col := b.rowCol()
	if row != 2 || col != 4 {
		t.Fatal(row, col)
	}
	b.vertical(-2)
	if b.Cursor != 2 {
		t.Fatal("desired visual column lost", b.Cursor)
	}
	if !b.goTo(3, 6) || b.goTo(4, 1) || b.goTo(1, 99) {
		t.Fatal("goto bounds")
	}
}
func TestSearchWrapAndReplace(t *testing.T) {
	b := bufferWith("日本abc 日本abc")
	if !b.find("日本abc") || b.Cursor != 6 {
		t.Fatal(b.Cursor)
	}
	if !b.find("日本abc") || b.Cursor != 0 {
		t.Fatal("wrap", b.Cursor)
	}
	b = bufferWith("abc")
	if !b.find("abc") || b.Cursor != 0 {
		t.Fatal("match crossing wrap boundary")
	}
	if b.find("missing") || b.find("") {
		t.Fatal("false match")
	}
	b = bufferWith("one one")
	if b.replaceAll("one", "二") != 2 || string(b.Text) != "二 二" {
		t.Fatal(string(b.Text))
	}
	b.undoEdit()
	if string(b.Text) != "one one" {
		t.Fatal("replace must be one undo")
	}
}
func TestCutLines(t *testing.T) {
	b := bufferWith("one\ntwo\n")
	b.Cursor = 2
	if b.cutLine() != "one\n" || string(b.Text) != "two\n" {
		t.Fatal(string(b.Text))
	}
	b.Cursor = len(b.Text)
	if b.cutLine() != "" {
		t.Fatal("empty final line")
	}
	b.undoEdit()
	if string(b.Text) != "one\ntwo\n" {
		t.Fatal("no-op must not add undo")
	}
}
func TestBoundedHistory(t *testing.T) {
	b := newBuffer()
	for i := 0; i < historyLimit+50; i++ {
		b.insert("x")
	}
	if len(b.undo) > historyLimit {
		t.Fatal(len(b.undo))
	}
	h := boundedHistory(nil, snapshot{text: strings.Repeat("x", historyBytes+1)})
	if len(h) != 0 {
		t.Fatal("history byte budget")
	}
}
