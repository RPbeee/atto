package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func syntaxAt(b *Buffer, needle string) tcell.Style {
	text := string(b.Text)
	i := strings.Index(text, needle)
	if i < 0 {
		panic("test token missing: " + needle)
	}
	return b.highlight().styleAt(len([]rune(text[:i])), normalStyle)
}
func TestSyntaxGoMultilineAndUnicode(t *testing.T) {
	b := bufferWith("package main\n/* 日本語\nreturn 123\n*/\nfunc main() {\n s := `日本\nsecond`\n n := 42\n}\n")
	b.Path = "main.go"
	if b.highlight().language != "Go" {
		t.Fatal(b.highlight().language)
	}
	if syntaxAt(b, "package") != keywordStyle || syntaxAt(b, "func") != keywordStyle || syntaxAt(b, "123") != commentStyle || syntaxAt(b, "second") != stringStyle || syntaxAt(b, "42") != numberStyle {
		t.Fatal("Go styles/context")
	}
	if syntaxAt(b, "main()") != functionStyle {
		t.Fatal("function colour")
	}
}
func TestSyntaxLanguagesAndPlainFallback(t *testing.T) {
	for _, fixture := range []struct {
		path, text, needle string
		style              tcell.Style
	}{
		{"script.py", "# hello\ndef f():\n return '日本'", "def", keywordStyle},
		{"app.ts", "const value = \"hello\";", "\"hello\"", stringStyle},
		{"data.json", "{\"name\": 42}", "42", numberStyle},
		{"style.css", "/* comment */\na { color: red; }", "comment", commentStyle},
		{"page.html", "<!-- comment -->\n<div>text</div>", "comment", commentStyle},
		{"README.md", "# Heading\n", "Heading", headingStyle},
	} {
		t.Run(fixture.path, func(t *testing.T) {
			b := bufferWith(fixture.text)
			b.Path = fixture.path
			if syntaxAt(b, fixture.needle) != fixture.style {
				t.Fatal(b.highlight().language, fixture.needle, syntaxAt(b, fixture.needle))
			}
		})
	}
	b := bufferWith("func main() { return 42 }")
	b.Path = "unknown.random-extension"
	if b.highlight().language != "Plain" || len(b.highlight().spans) != 0 {
		t.Fatal("unknown file inferred language")
	}
	b = bufferWith("#!/usr/bin/env python3\nprint('hello')\n")
	if !strings.Contains(strings.ToLower(b.highlight().language), "python") {
		t.Fatal("shebang", b.highlight().language)
	}
}
func TestSyntaxEditUndoRenameAndManualLanguage(t *testing.T) {
	b := bufferWith("/* comment\nreturn 42\n")
	b.Path = "main.go"
	if syntaxAt(b, "return") != commentStyle {
		t.Fatal("initial block comment")
	}
	b.Cursor = len([]rune("/* comment"))
	b.insert(" */")
	if syntaxAt(b, "return") != keywordStyle {
		t.Fatal("edit did not invalidate multiline context")
	}
	b.undoEdit()
	if syntaxAt(b, "return") != commentStyle {
		t.Fatal("undo invalidation")
	}
	b.redoEdit()
	if syntaxAt(b, "return") != keywordStyle {
		t.Fatal("redo invalidation")
	}
	b.Path = "main.unknown-extension"
	if len(b.highlight().spans) != 0 {
		t.Fatal("rename kept old lexer")
	}
	b.SyntaxLanguage = "go"
	if syntaxAt(b, "return") != keywordStyle {
		t.Fatal("explicit language")
	}
	b.SyntaxLanguage = "off"
	if len(b.highlight().spans) != 0 || b.dirty() == false {
		t.Fatal("manual off changed text state")
	}
}
func TestSyntaxLimitAndNoTextMutation(t *testing.T) {
	for _, text := range []string{strings.Repeat("x", syntaxMaxBytes+1), strings.Repeat("日", syntaxMaxBytes/3+1)} {
		b := bufferWith(text)
		b.Path = "main.go"
		if !b.highlight().skipped || len(b.highlight().spans) != 0 || b.dirty() || string(b.Text) != text {
			t.Fatal("large document fallback")
		}
	}
	for _, text := range []string{"\n\npackage main", "package main\n", "", "func main() { s := \"unterminated"} {
		b := bufferWith(text)
		b.Path = "main.go"
		b.highlight()
		if b.dirty() || string(b.Text) != text {
			t.Fatal("lexer altered text")
		}
	}
}
func TestSyntaxRenderSplitSelectionAndToggle(t *testing.T) {
	e := testEditor(t)
	b := e.current()
	b.Path = "main.go"
	typed(e, "package main\n// 日本\n")
	b.goTo(1, 1)
	e.draw()
	screen := e.screen.(tcell.SimulationScreen)
	_, _, style, _ := screen.GetContent(3, 2)
	if style != keywordStyle {
		t.Fatal("render keyword", style)
	}
	key(e, tcell.KeyCtrlSpace)
	key(e, tcell.KeyRight)
	e.draw()
	_, _, style, _ = screen.GetContent(3, 2)
	if style != keywordStyle.Background(tcell.ColorDarkCyan) {
		t.Fatal("linear selection lost keyword foreground")
	}
	key(e, tcell.KeyEscape)
	b.goTo(1, 1)
	alt(e, 'r')
	key(e, tcell.KeyRight)
	e.draw()
	_, _, style, _ = screen.GetContent(3, 2)
	if style != keywordStyle.Background(tcell.ColorDarkCyan) {
		t.Fatal("rectangle lost syntax foreground")
	}
	key(e, tcell.KeyEscape)
	key(e, tcell.KeyF3)
	e.draw()
	for _, x := range []int{3, 43} {
		_, _, style, _ = screen.GetContent(x, 3)
		if style != keywordStyle {
			t.Fatal("split colour missing", x, style)
		}
	}
	key(e, tcell.KeyF9)
	e.draw()
	_, _, style, _ = screen.GetContent(3, 3)
	if style != normalStyle {
		t.Fatal("toggle off")
	}
	alt(e, 'h')
	e.draw()
	_, _, style, _ = screen.GetContent(3, 3)
	if style != keywordStyle {
		t.Fatal("toggle on")
	}
}
func TestSyntaxLanguagePromptSaveAsAndUnicodeScroll(t *testing.T) {
	e := testEditor(t)
	typed(e, "日本 日本 const x = 42;")
	b := e.current()
	alt(e, 'l')
	key(e, tcell.KeyCtrlU)
	answer(e, "javascript")
	if syntaxAt(b, "const") != keywordStyle {
		t.Fatal("manual language prompt", b.SyntaxLanguage)
	}
	alt(e, 'l')
	key(e, tcell.KeyCtrlU)
	answer(e, "not-a-language")
	if b.SyntaxLanguage != "JavaScript" || !strings.Contains(e.message, "Unknown language") {
		t.Fatal("unknown language changed lexer", b.SyntaxLanguage)
	}
	alt(e, 'l')
	key(e, tcell.KeyCtrlU)
	answer(e, "auto")
	if len(b.highlight().spans) != 0 {
		t.Fatal("auto unnamed buffer")
	}
	path := filepath.Join(t.TempDir(), "test.js")
	e.saveBuffer(b, true, nil)
	answer(e, path)
	if syntaxAt(b, "const") != keywordStyle || b.dirty() {
		t.Fatal("save as did not infer new language")
	}
	screen := e.screen.(tcell.SimulationScreen)
	screen.SetSize(24, 8)
	b.Cursor = len(b.Text)
	e.draw()
	if b.Left == 0 {
		t.Fatal("did not exercise horizontal scroll")
	}
	b.goTo(1, 7)
	e.draw()
	cell := b.cursorCell() - b.Left + 3
	_, _, style, _ := screen.GetContent(cell, 2)
	if style != keywordStyle {
		t.Fatal("Unicode rune/cell offsets lost colour", cell, style)
	}
}

func TestGoUnfinishedStringsAndEscapes(t *testing.T) {
	for _, text := range []string{"s := `日本\nreturn 42", "s := \"日本", "c := '日"} {
		b := bufferWith(text)
		b.Path = "main.go"
		if syntaxAt(b, "日") != stringStyle {
			t.Fatal("unfinished literal", text)
		}
	}
	b := bufferWith("s := \"日本\\\"x\"; return 42")
	b.Path = "main.go"
	if syntaxAt(b, "x") != stringStyle || syntaxAt(b, "return") != keywordStyle {
		t.Fatal("escaped quote swallowed later code")
	}
	b = bufferWith("s := \"unfinished\nreturn 42")
	b.Path = "main.go"
	if syntaxAt(b, "return") != keywordStyle {
		t.Fatal("ordinary string incorrectly spanned newline")
	}
}
func TestAssemblyCommentForms(t *testing.T) {
	for _, path := range []string{"start.S", "start.s"} {
		b := bufferWith("# hash line\n  movq $1, %rax # trailing\n  mov r0, #1 @ at\n  b done // slash\n  /* block\n  rax */\n  nop ; semi\n")
		b.Path = path
		for _, needle := range []string{"hash", "trailing", "at\n", "slash", "block", "semi"} {
			if syntaxAt(b, needle) != commentStyle {
				t.Fatalf("%s: %q is not a comment", path, needle)
			}
		}
		if syntaxAt(b, "#1") == commentStyle {
			t.Fatalf("%s: ARM immediate coloured as comment", path)
		}
	}
}
func TestCommentsInFilesChromaMisreads(t *testing.T) {
	for _, fixture := range []struct{ path, text string }{
		{"kernel.cu", "int x; // CMT\n"},
		{"view.mm", "int x; // CMT\n"},
		{"shader.glsl", "void main() {} // CMT\n"},
		{"style.less", "a { color: red; } // CMT\n"},
		{"app.conf", "key value # CMT\n"},
		{"app.conf", "# CMT\nkey value\n"},
		{".gitignore", "build/\n# CMT\n"},
		{"go.mod", "module x\n\ngo 1.25 // CMT\n"},
		{"a.m", "#import <Foundation/Foundation.h>\nint x; // CMT\n"},
		{"a.m", "x = 1; % CMT\n"},
		{"a.v", "module m; endmodule // CMT\n"},
		{"a.v", "Require Import X. (* CMT *)\n"},
	} {
		b := bufferWith(fixture.text)
		b.Path = fixture.path
		if syntaxAt(b, "CMT") != commentStyle {
			t.Fatalf("%s: comment not coloured (language %s)", fixture.path, b.highlight().language)
		}
	}
	b := bufferWith("key = #1\n")
	b.Path = "app.conf"
	if syntaxAt(b, "#1") == commentStyle {
		t.Fatal("hash without space coloured as comment")
	}
}
func TestMakefileDirectivesAndIntelAssembly(t *testing.T) {
	b := bufferWith("# top\nifeq ($(OS),Linux)\nCC := gcc # trail\nelse\nCC := cc\nendif\ninclude config.mk # inc\ndefine X\nendef\n\nall:\n\t$(CC) -o $@ # recipe\n")
	b.Path = "Makefile"
	for _, word := range []string{"ifeq", "else", "endif", "include", "define", "endef"} {
		if syntaxAt(b, word) != keywordStyle {
			t.Fatalf("%s not a keyword", word)
		}
	}
	for _, word := range []string{"top", "trail", "# inc", "recipe"} {
		if syntaxAt(b, word) != commentStyle {
			t.Fatalf("%s not a comment", word)
		}
	}
	if syntaxAt(b, "ifeq ($") == errorStyle || syntaxAt(b, "Linux") == errorStyle {
		t.Fatal("directive arguments coloured as errors")
	}
	asm := bufferWith(".intel_syntax noprefix\nmain:\n  mov rax, QWORD PTR [rbp-8] # c1\n  lea rdi, [rip+msg] // c2\n  call puts@PLT ; c3\n  add rax, 1 # c4\n")
	asm.Path = "start.s"
	if asm.highlight().language != "GAS" {
		t.Fatal(asm.highlight().language)
	}
	for _, word := range []string{"c1", "c2", "c3", "c4"} {
		if syntaxAt(asm, word) != commentStyle {
			t.Fatalf("%s not a comment", word)
		}
	}
	for _, spot := range []string{"+msg", "rax, QWORD"} {
		if syntaxAt(asm, spot) == errorStyle {
			t.Fatalf("%q coloured as error", spot)
		}
	}
	nasm := bufferWith("section .text\n_start:\n  mov eax, [ebp+8] ; c\n")
	nasm.Path = "x.asm"
	if syntaxAt(nasm, "; c") != commentStyle || syntaxAt(nasm, "ebp") == errorStyle {
		t.Fatal("NASM Intel syntax")
	}
}
