package main

import (
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/gdamore/tcell/v2"
)

// Full-document lexing preserves context across multi-line comments/strings.
// Larger documents retain normal editing without spending time on tokenisation.
const syntaxMaxBytes = 256 << 10

type syntaxSpan struct {
	start, end int
	style      tcell.Style
}
type syntaxCache struct {
	ready                    bool
	revision                 uint64
	path, override, language string
	skipped                  bool
	spans                    []syntaxSpan
}

var (
	keywordStyle  = normalStyle.Foreground(tcell.ColorLightSkyBlue).Bold(true)
	stringStyle   = normalStyle.Foreground(tcell.ColorLightGreen)
	commentStyle  = normalStyle.Foreground(tcell.ColorGray)
	numberStyle   = normalStyle.Foreground(tcell.ColorLightSalmon)
	functionStyle = normalStyle.Foreground(tcell.ColorLightYellow)
	typeStyle     = normalStyle.Foreground(tcell.ColorLightCyan)
	operatorStyle = normalStyle.Foreground(tcell.ColorSilver)
	headingStyle  = normalStyle.Foreground(tcell.ColorLightSkyBlue).Bold(true)
	errorStyle    = normalStyle.Foreground(tcell.ColorLightCoral)
)

// Go's stock lexer waits for closing delimiters. These editor rules also colour
// the unfinished comment/string currently being typed, up to EOF or line end.
var editingGoLexer = chroma.MustNewLexer(lexers.Go.Config(), func() chroma.Rules {
	rules := lexers.Go.(*chroma.RegexLexer).MustRules().Clone()
	prefix := []chroma.Rule{
		{Pattern: `(?s:/\*.*?(?:\*/|\z))`, Type: chroma.CommentMultiline},
		{Pattern: "(?s:`[^`]*(?:`|\\z))", Type: chroma.LiteralString},
		{Pattern: `"(?:\\.|[^"\\\n])*(?:"|\n|\z)`, Type: chroma.LiteralString},
		{Pattern: `'(?:\\.|[^'\\\n])*(?:'|\n|\z)`, Type: chroma.LiteralStringChar},
	}
	rules["root"] = append(prefix, rules["root"]...)
	return rules
}).SetRegistry(lexers.GlobalLexerRegistry)

// Chroma maps *.s / *.S to the ARM lexer, whose comment rules miss "#" and "//"
// used by x86, RISC-V and AArch64 GNU assembly (they would show as errors).
// These rules colour every common GNU assembler comment form. "#" is a comment
// only at line start or before whitespace so ARM immediates such as "#1" stay code.
// They go first in the states that read instructions, never in string literals.
func assemblyLexer(base chroma.Lexer) chroma.Lexer {
	regex, ok := base.(*chroma.RegexLexer)
	if !ok {
		return base
	}
	return chroma.MustNewLexer(base.Config(), func() chroma.Rules {
		rules := regex.MustRules().Clone()
		prefix := []chroma.Rule{
			{Pattern: `(?s:/\*.*?(?:\*/|\z))`, Type: chroma.CommentMultiline},
			{Pattern: `//[^\n]*\n?`, Type: chroma.CommentSingle},
			{Pattern: `(?m:(?<=^[ \t]*)#[^\n]*\n?)`, Type: chroma.CommentSingle},
			{Pattern: `(?<=[ \t])#(?=[ \t\n#]|\z)[^\n]*\n?`, Type: chroma.CommentSingle},
		}
		for _, state := range []string{"root", "opcode"} {
			rules[state] = append(append([]chroma.Rule{}, prefix...), rules[state]...)
		}
		return rules
	}).SetRegistry(lexers.GlobalLexerRegistry)
}

var assemblyLexers = map[string]chroma.Lexer{}

func init() {
	for _, name := range []string{"ArmAsm"} {
		if base := lexers.Get(name); base != nil {
			assemblyLexers[name] = assemblyLexer(base)
		}
	}
}

// Chroma has no lexer for these names, or picks the wrong language for them, so
// files stayed plain or showed "//" and "#" comments as ordinary text.
var extensionLexers = map[string]string{
	".cu": "cpp", ".cuh": "cpp", ".ixx": "cpp", ".cppm": "cpp", ".mm": "objective-c",
	".glsl": "glsl", ".comp": "glsl", ".tesc": "glsl", ".tese": "glsl", ".less": "scss",
	".fsx": "fsharp", ".cljs": "clojure", ".cljc": "clojure", ".ron": "rust",
	".tfvars": "terraform", ".jinja": "jinja", ".jinja2": "jinja", ".j2": "jinja",
	".mdx": "markdown", ".profile": "bash",
}

// hashCommentLexer colours "#" comments in config-like files that have no grammar.
var hashCommentLexer = chroma.MustNewLexer(&chroma.Config{Name: "Config"}, func() chroma.Rules {
	return chroma.Rules{"root": {
		{Pattern: `(?m:(?<=^[ \t]*)#[^\n]*\n?)`, Type: chroma.CommentSingle},
		{Pattern: `(?<=[ \t])#(?=[ \t]|\n|\z)[^\n]*\n?`, Type: chroma.CommentSingle},
		{Pattern: `[^#\n]+|#|\n`, Type: chroma.Text},
	}}
})
var hashCommentFiles = map[string]bool{".conf": true, ".gitignore": true, ".dockerignore": true,
	".npmignore": true, ".gitattributes": true, ".gitmodules": true, ".ignore": true}

func containsAny(text string, words ...string) bool {
	for _, w := range words {
		if strings.Contains(text, w) {
			return true
		}
	}
	return false
}

// ambiguousLexer settles extensions shared by several languages from the content.
func ambiguousLexer(name string, text []rune) chroma.Lexer {
	sample := string(text[:min(len(text), 8192)])
	switch name {
	case "go.mod", "go.work":
		return lexers.Get("go")
	case ".m": // Objective-C, MATLAB/Octave or Mathematica
		switch {
		case containsAny(sample, "#import", "@interface", "@implementation", "@end", "@property", "NSString"):
			return lexers.Get("objective-c")
		case containsAny(sample, "(*", ":=", "[["):
			return lexers.Get("mathematica")
		default:
			return lexers.Get("matlab")
		}
	case ".v": // Verilog, Coq or V
		switch {
		case containsAny(sample, "endmodule"):
			return lexers.Get("verilog")
		case containsAny(sample, "Require ", "Definition ", "Lemma ", "Theorem ", "Inductive ", "Fixpoint ", "Proof."):
			return lexers.Get("coq")
		case containsAny(sample, "fn ", "import ", ":="):
			return lexers.Get("v")
		default:
			return lexers.Get("verilog")
		}
	}
	return nil
}
func fallbackLexer(base string, text []rune) chroma.Lexer {
	ext := strings.ToLower(filepath.Ext(base))
	if lexer := ambiguousLexer(base, text); lexer != nil {
		return lexer
	}
	if lexer := ambiguousLexer(ext, text); lexer != nil {
		return lexer
	}
	if hashCommentFiles[base] || hashCommentFiles[ext] {
		return hashCommentLexer
	}
	if name, ok := extensionLexers[ext]; ok {
		return lexers.Get(name)
	}
	return nil
}

func shebangLexer(line string) chroma.Lexer {
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return nil
	}
	interpreter := filepath.Base(fields[0])
	if interpreter == "env" {
		interpreter = ""
		for _, field := range fields[1:] {
			if !strings.HasPrefix(field, "-") && !strings.Contains(field, "=") {
				interpreter = filepath.Base(field)
				break
			}
		}
	}
	switch {
	case strings.HasPrefix(interpreter, "python"):
		return lexers.Get("python")
	case interpreter == "sh" || interpreter == "bash" || interpreter == "zsh" || interpreter == "ksh":
		return lexers.Get("bash")
	case interpreter == "node" || interpreter == "nodejs":
		return lexers.Get("javascript")
	case interpreter == "ruby" || interpreter == "perl" || interpreter == "php" || interpreter == "fish":
		return lexers.Get(interpreter)
	default:
		return nil
	}
}

func tokenStyle(token chroma.TokenType) tcell.Style {
	switch {
	case token.InCategory(chroma.Comment):
		return commentStyle
	case token.InCategory(chroma.Keyword):
		return keywordStyle
	case token.InSubCategory(chroma.LiteralString):
		return stringStyle
	case token.InSubCategory(chroma.LiteralNumber):
		return numberStyle
	case token.InSubCategory(chroma.NameFunction):
		return functionStyle
	case token == chroma.NameClass || token == chroma.NameBuiltin || token == chroma.NameTag || token == chroma.NameAttribute:
		return typeStyle
	case token.InCategory(chroma.Operator):
		return operatorStyle
	case token == chroma.GenericHeading || token == chroma.GenericSubheading:
		return headingStyle
	case token == chroma.GenericStrong:
		return normalStyle.Bold(true)
	case token == chroma.GenericEmph:
		return normalStyle.Italic(true)
	case token == chroma.GenericInserted:
		return stringStyle
	case token == chroma.GenericDeleted || token == chroma.Error:
		return errorStyle
	default:
		return normalStyle
	}
}
func (s *syntaxCache) styleAt(pos int, fallback tcell.Style) tcell.Style {
	if s == nil {
		return fallback
	}
	i := sort.Search(len(s.spans), func(i int) bool { return s.spans[i].end > pos })
	if i < len(s.spans) && s.spans[i].start <= pos {
		return s.spans[i].style
	}
	return fallback
}
func (b *Buffer) syntaxLexer() chroma.Lexer {
	if b.SyntaxLanguage == "off" {
		return nil
	}
	if b.SyntaxLanguage != "" && b.SyntaxLanguage != "auto" {
		return lexers.Get(b.SyntaxLanguage)
	}
	if b.Path != "" {
		base := filepath.Base(b.Path)
		// Ambiguous or unsupported names are decided before Chroma's own match.
		if lexer := ambiguousLexer(base, b.Text); lexer != nil {
			return lexer
		}
		if ext := strings.ToLower(filepath.Ext(base)); ext == ".m" || ext == ".v" {
			return ambiguousLexer(ext, b.Text)
		}
		if lexer := lexers.Match(base); lexer != nil {
			return lexer
		}
		if lexer := fallbackLexer(base, b.Text); lexer != nil {
			return lexer
		}
	}
	// Content detection is intentionally limited to explicit interpreter shebangs.
	if len(b.Text) >= 2 && b.Text[0] == '#' && b.Text[1] == '!' {
		end := min(len(b.Text), 512)
		for i := 0; i < end; i++ {
			if b.Text[i] == '\n' {
				end = i
				break
			}
		}
		return shebangLexer(string(b.Text[:end]))
	}
	return nil
}
func (b *Buffer) highlight() *syntaxCache {
	s := &b.syntax
	if s.ready && s.revision == b.revision && s.path == b.Path && s.override == b.SyntaxLanguage {
		return s
	}
	*s = syntaxCache{ready: true, revision: b.revision, path: b.Path, override: b.SyntaxLanguage, language: "Plain"}
	lexer := b.syntaxLexer()
	if lexer == nil {
		return s
	}
	s.language = lexer.Config().Name
	if len(b.Text) > syntaxMaxBytes {
		s.skipped = true
		return s
	}
	text := string(b.Text)
	if len(text) > syntaxMaxBytes {
		s.skipped = true
		return s
	}
	// Any lexer failure affects colouring only; the editable text is never replaced.
	defer func() {
		if recover() != nil {
			s.spans = nil
			s.skipped = true
		}
	}()
	if lexer.Config().Name == "Go" {
		lexer = editingGoLexer
	} else if asm, ok := assemblyLexers[lexer.Config().Name]; ok {
		lexer = asm
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(&chroma.TokeniseOptions{State: "root", EnsureLF: false}, text)
	if err != nil {
		s.skipped = true
		return s
	}
	offset := 0
	for token := iterator(); token != chroma.EOF; token = iterator() {
		count := utf8.RuneCountInString(token.Value)
		end := min(len(b.Text), offset+count)
		style := tokenStyle(token.Type)
		if end > offset && style != normalStyle {
			if n := len(s.spans); n > 0 && s.spans[n-1].end == offset && s.spans[n-1].style == style {
				s.spans[n-1].end = end
			} else {
				s.spans = append(s.spans, syntaxSpan{offset, end, style})
			}
		}
		offset += count
		if offset >= len(b.Text) {
			break
		}
	}
	return s
}
func (e *Editor) chooseSyntax() {
	b := e.current()
	current := b.SyntaxLanguage
	if current == "" {
		current = "auto"
	}
	e.ask("Language (auto/off/go/python/...):", current, func(language string) {
		language = strings.TrimSpace(language)
		if strings.EqualFold(language, "auto") || language == "" {
			b.SyntaxLanguage = "auto"
		} else if strings.EqualFold(language, "off") {
			b.SyntaxLanguage = "off"
		} else {
			lexer := lexers.Get(language)
			if lexer == nil {
				e.message = "Unknown language: " + language
				return
			}
			b.SyntaxLanguage = lexer.Config().Name
		}
		e.message = "Language: " + b.highlight().language
	})
}
