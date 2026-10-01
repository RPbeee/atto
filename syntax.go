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
		if lexer := lexers.Match(filepath.Base(b.Path)); lexer != nil {
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
