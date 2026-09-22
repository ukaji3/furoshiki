// Package css rewrites the URL references inside style sheets: url() values,
// the string arguments of image-set(), and the targets of @import rules.
//
// The style sheet is tokenized with github.com/tdewolff/parse/v2/css and
// re-emitted token by token, so everything that is not a rewritten reference
// (selectors, declarations, comments, whitespace, even syntax errors) is
// preserved byte for byte.
package css

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"

	"github.com/ukaji3/furoshiki/internal/textenc"
)

// Handler receives the references found by Rewrite and decides how to
// replace them. Returning ok=false leaves the reference unchanged.
type Handler interface {
	// URL handles url() values and image-set() strings. raw is the URL with
	// CSS quoting and escapes removed.
	URL(raw string) (replacement string, ok bool)
	// Import handles the target of an @import rule. Only the URL itself is
	// replaced; layer(), supports() and media conditions that follow it are
	// preserved verbatim.
	Import(raw string) (replacement string, ok bool)
}

// Rewrite tokenizes src and returns it with references replaced according to
// h. Replacements are emitted as url("...") with CSS string escaping. All
// other bytes are copied verbatim, so Rewrite with a Handler that never
// replaces anything returns a copy of src.
func Rewrite(src []byte, h Handler) []byte {
	l := css.NewLexer(parse.NewInputBytes(src))
	out := make([]byte, 0, len(src)+len(src)/8)
	var (
		inImport bool     // between "@import" and its URL
		stack    []string // lower-cased names of open functions ("" for bare parentheses)
	)
	inImageSet := func() bool {
		return len(stack) > 0 && (stack[len(stack)-1] == "image-set" || stack[len(stack)-1] == "-webkit-image-set")
	}
	for {
		tt, text := l.Next()
		switch tt {
		case css.ErrorToken:
			// io.EOF, or an unrecoverable lexer error; either way the input
			// has been consumed as far as it can be.
			return out
		case css.AtKeywordToken:
			inImport = strings.EqualFold(string(text), "@import")
		case css.SemicolonToken, css.LeftBraceToken, css.RightBraceToken:
			inImport = false
		case css.FunctionToken:
			stack = append(stack, strings.ToLower(strings.TrimSuffix(string(text), "(")))
		case css.LeftParenthesisToken:
			stack = append(stack, "")
		case css.RightParenthesisToken:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case css.URLToken:
			raw := unwrapURL(text)
			if inImport {
				inImport = false
				if rep, ok := h.Import(raw); ok {
					out = appendURL(out, rep)
					continue
				}
			} else if rep, ok := h.URL(raw); ok {
				out = appendURL(out, rep)
				continue
			}
		case css.StringToken:
			if inImport {
				inImport = false
				if rep, ok := h.Import(Unquote(string(text))); ok {
					out = appendURL(out, rep)
					continue
				}
			} else if inImageSet() {
				if rep, ok := h.URL(Unquote(string(text))); ok {
					out = appendURL(out, rep)
					continue
				}
			}
		}
		out = append(out, text...)
	}
}

// Decode converts a style sheet's bytes to UTF-8 text following the CSS
// encoding rules: a byte order mark wins, then an @charset rule at the very
// start of the sheet, then the given fallback labels in order (typically the
// charset attribute of the referring element followed by the encoding of the
// referring document). The @charset rule is removed from the result. name is
// the canonical name of the encoding that was used.
func Decode(data []byte, fallbacks ...string) (text, name string) {
	labels := fallbacks
	if l := textenc.SniffCSS(data); l != "" {
		labels = append([]string{l}, fallbacks...)
	}
	text, name = textenc.Decode(data, labels...)
	return textenc.StripCSSCharset(text), name
}

// appendURL appends url("rep") with rep escaped as a CSS string.
func appendURL(out []byte, rep string) []byte {
	out = append(out, `url("`...)
	out = append(out, Escape(rep)...)
	return append(out, `")`...)
}

// unwrapURL extracts the URL from a url(...) token, handling optional
// whitespace, optional quotes and CSS escapes. The closing parenthesis may be
// missing when the token ends the input.
func unwrapURL(tok []byte) string {
	s := string(tok)
	if i := strings.IndexByte(s, '('); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ")")
	s = strings.Trim(s, " \t\n\r\f")
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		return Unquote(s)
	}
	return unescape(s, false)
}

// Unquote removes the quotes around a CSS string token and resolves its
// escape sequences. A token without quotes is returned with escapes resolved.
func Unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') {
		if s[len(s)-1] == s[0] {
			s = s[1 : len(s)-1]
		} else {
			s = s[1:] // unterminated string at end of input
		}
		return unescape(s, true)
	}
	return unescape(s, false)
}

// unescape resolves CSS escape sequences. Inside strings a backslash before
// a newline is a line continuation and produces nothing.
func unescape(s string, inString bool) string {
	if !strings.Contains(s, "\\") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		i++
		if i >= len(s) {
			// A lone backslash at the end of input is dropped, as browsers do.
			break
		}
		c = s[i]
		switch {
		case isHex(c):
			j := i
			var r rune // at most 6 hex digits, so r <= 0xFFFFFF
			for j < len(s) && j < i+6 && isHex(s[j]) {
				r = r<<4 | rune(hexVal(s[j]))
				j++
			}
			if r == 0 || r > utf8.MaxRune || (r >= 0xD800 && r <= 0xDFFF) {
				r = utf8.RuneError
			}
			b.WriteRune(r)
			i = j - 1
			// A single whitespace character after a hex escape is part of
			// the escape.
			if j < len(s) {
				switch s[j] {
				case '\r':
					i = j
					if j+1 < len(s) && s[j+1] == '\n' {
						i = j + 1
					}
				case ' ', '\t', '\n', '\f':
					i = j
				}
			}
		case c == '\n' || c == '\f' || c == '\r':
			if inString {
				if c == '\r' && i+1 < len(s) && s[i+1] == '\n' {
					i++
				}
				continue
			}
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			_, size := utf8.DecodeRuneInString(s[i:])
			b.WriteString(s[i : i+size])
			i += size - 1
		}
	}
	return b.String()
}

// Escape escapes s for use inside a double-quoted CSS string.
func Escape(s string) string {
	if !strings.ContainsAny(s, "\"\\\n\r\f\x00") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n', '\r', '\f', 0:
			b.WriteString(`\`)
			b.WriteString(strconv.FormatInt(int64(r), 16))
			b.WriteByte(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// hexVal returns the value of a hexadecimal digit; c must satisfy isHex.
func hexVal(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	default:
		return c - 'a' + 10
	}
}
