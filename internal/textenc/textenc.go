// Package textenc decodes site files (HTML, CSS, JavaScript) to UTF-8 using
// the encoding declarations that browsers honour: byte order marks,
// <meta charset> declarations, @charset rules, and caller-supplied fallbacks.
package textenc

import (
	"bytes"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/charset"
	"golang.org/x/text/encoding"
)

// Lookup resolves an encoding label the way the HTML specification requires:
// labels are matched case-insensitively against the WHATWG Encoding
// Standard, declarations of UTF-16 are treated as UTF-8 (a 16-bit encoding
// can only be signalled by a byte order mark), and x-user-defined maps to
// windows-1252. ok is false for unknown labels.
func Lookup(label string) (enc encoding.Encoding, name string, ok bool) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, "", false
	}
	enc, name = charset.Lookup(label)
	if enc == nil {
		return nil, "", false
	}
	switch name {
	case "utf-16be", "utf-16le":
		enc, name = charset.Lookup("utf-8")
	case "x-user-defined":
		enc, name = charset.Lookup("windows-1252")
	}
	return enc, name, true
}

// BOM returns the canonical name of the encoding signalled by a byte order
// mark at the start of data, or "" when there is none.
func BOM(data []byte) string {
	switch {
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		return "utf-8"
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return "utf-16be"
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return "utf-16le"
	}
	return ""
}

// Decode converts data to UTF-8 text.
//
// The encoding is chosen in this order: a byte order mark; the first label in
// labels that names a known encoding; UTF-8. Invalid byte sequences are
// replaced by U+FFFD and a leading U+FEFF is removed, so the result is always
// valid UTF-8 without a byte order mark. name is the canonical name of the
// encoding that was used.
func Decode(data []byte, labels ...string) (text, name string) {
	var enc encoding.Encoding
	if bom := BOM(data); bom != "" {
		enc, name = charset.Lookup(bom)
	} else {
		for _, l := range labels {
			if e, n, ok := Lookup(l); ok {
				enc, name = e, n
				break
			}
		}
		if enc == nil {
			enc, name = charset.Lookup("utf-8")
		}
	}
	out, err := enc.NewDecoder().Bytes(data)
	if err != nil {
		out = data
	}
	s := strings.TrimPrefix(string(out), "\uFEFF")
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "\uFFFD")
	}
	return s, name
}

// prescanLimit is the number of leading bytes the HTML encoding prescan
// examines, as specified by the HTML standard.
const prescanLimit = 1024

var charsetParam = regexp.MustCompile(`(?i)charset\s*=\s*["']?\s*([^\s"';,]+)`)

// SniffHTML looks for an encoding declaration in a <meta> element within the
// first 1024 bytes of an HTML document and returns its label, or "" when
// there is none. Both <meta charset="..."> and
// <meta http-equiv="Content-Type" content="...; charset=..."> are
// recognised. The label is returned as written; pass it to Lookup.
func SniffHTML(data []byte) string {
	if len(data) > prescanLimit {
		data = data[:prescanLimit]
	}
	z := html.NewTokenizer(bytes.NewReader(data))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return ""
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "meta" || !hasAttr {
				continue
			}
			var charsetAttr, httpEquiv, content string
			for {
				k, v, more := z.TagAttr()
				switch string(k) {
				case "charset":
					charsetAttr = string(v)
				case "http-equiv":
					httpEquiv = string(v)
				case "content":
					content = string(v)
				}
				if !more {
					break
				}
			}
			if l := strings.TrimSpace(charsetAttr); l != "" {
				return l
			}
			if strings.EqualFold(strings.TrimSpace(httpEquiv), "content-type") {
				if m := charsetParam.FindStringSubmatch(content); m != nil {
					return m[1]
				}
			}
		}
	}
}

var cssCharsetRule = regexp.MustCompile(`^@charset "([^"]*)";`)

// SniffCSS returns the label of an @charset rule at the very start of a
// style sheet, or "" when there is none. Following the CSS specification the
// rule must be spelled exactly `@charset "label";` with no preceding bytes.
func SniffCSS(data []byte) string {
	m := cssCharsetRule.FindSubmatch(data)
	if m == nil {
		return ""
	}
	return string(m[1])
}

var cssCharsetPrefix = regexp.MustCompile(`^\s*@charset\s+"[^"]*"\s*;\s*`)

// StripCSSCharset removes a leading @charset rule from decoded CSS text. The
// rule has no effect once the text is UTF-8 and would be misleading if kept.
func StripCSSCharset(text string) string {
	return cssCharsetPrefix.ReplaceAllString(text, "")
}
