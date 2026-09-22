package css

import (
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
)

// recorder is a Handler that records the raw references it sees and replaces
// them with a fixed prefix so the rewritten output can be asserted on.
type recorder struct {
	urls, imports []string
	skip          map[string]bool // raw values to leave unchanged
}

func (r *recorder) URL(raw string) (string, bool) {
	r.urls = append(r.urls, raw)
	if r.skip[raw] {
		return "", false
	}
	return "U:" + raw, true
}

func (r *recorder) Import(raw string) (string, bool) {
	r.imports = append(r.imports, raw)
	if r.skip[raw] {
		return "", false
	}
	return "I:" + raw, true
}

type identity struct{}

func (identity) URL(string) (string, bool)    { return "", false }
func (identity) Import(string) (string, bool) { return "", false }

func TestRewriteIdentity(t *testing.T) {
	inputs := []string{
		"",
		"body { color: red }",
		`@import url("a.css") screen; a { background: url(b.png) } /* url(c.png) */ .x::after { content: "url(d.png)" }`,
		"a{b:c", // unterminated block
		`a { background: url( "unterminated ) }`,
		"@media (min-width: 1px) { a { x: image-set(\"a.png\" 1x) } }",
		"<!-- a { } -->",
		"a{--x: url(a.png)}",
		"\xff\xfe binary junk \x00",
	}
	for _, in := range inputs {
		if got := string(Rewrite([]byte(in), identity{})); got != in {
			t.Errorf("Rewrite(%q) changed input to %q", in, got)
		}
	}
}

func TestRewriteURLs(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
		urls []string
	}{
		{
			name: "unquoted",
			in:   `a{background:url(img/a.png)}`,
			want: `a{background:url("U:img/a.png")}`,
			urls: []string{"img/a.png"},
		},
		{
			name: "double quoted",
			in:   `a{background:url("img/a b.png")}`,
			want: `a{background:url("U:img/a b.png")}`,
			urls: []string{"img/a b.png"},
		},
		{
			name: "single quoted with spaces around",
			in:   `a{background:url( 'a.png' )}`,
			want: `a{background:url("U:a.png")}`,
			urls: []string{"a.png"},
		},
		{
			name: "upper case function",
			in:   `a{background:URL(a.png)}`,
			want: `a{background:url("U:a.png")}`,
			urls: []string{"a.png"},
		},
		{
			name: "multiple in one declaration",
			in:   `a{background:url(a.png),url(b.png) no-repeat}`,
			want: `a{background:url("U:a.png"),url("U:b.png") no-repeat}`,
			urls: []string{"a.png", "b.png"},
		},
		{
			name: "font-face with fragment",
			in:   `@font-face{src:url(f.eot?#iefix) format("embedded-opentype"),url('f.woff2') format("woff2")}`,
			want: `@font-face{src:url("U:f.eot?#iefix") format("embedded-opentype"),url("U:f.woff2") format("woff2")}`,
			urls: []string{"f.eot?#iefix", "f.woff2"},
		},
		{
			name: "escapes in quoted string",
			in:   `a{background:url("a\"b\.png")}`,
			want: `a{background:url("U:a\"b.png")}`,
			urls: []string{`a"b.png`},
		},
		{
			name: "hex escape",
			in:   `a{background:url(a\20 b.png)}`,
			want: `a{background:url("U:a b.png")}`,
			urls: []string{"a b.png"},
		},
		{
			name: "escaped paren unquoted",
			in:   `a{background:url(a\).png)}`,
			want: `a{background:url("U:a).png")}`,
			urls: []string{"a).png"},
		},
		{
			name: "image-set strings",
			in:   `a{background:image-set("a.png" 1x, url(b.png) 2x, "c.png" type("image/png"))}`,
			want: `a{background:image-set(url("U:a.png") 1x, url("U:b.png") 2x, url("U:c.png") type("image/png"))}`,
			urls: []string{"a.png", "b.png", "c.png"},
		},
		{
			name: "webkit image-set",
			in:   `a{background:-webkit-image-set("a.png" 1x)}`,
			want: `a{background:-webkit-image-set(url("U:a.png") 1x)}`,
			urls: []string{"a.png"},
		},
		{
			name: "strings outside image-set untouched",
			in:   `a::after{content:"a.png"} b{font-family:"x.png"}`,
			want: `a::after{content:"a.png"} b{font-family:"x.png"}`,
		},
		{
			name: "nested function resets image-set",
			in:   `a{background:image-set(linear-gradient("x") 1x)}`,
			want: `a{background:image-set(linear-gradient("x") 1x)}`,
		},
		{
			name: "comment untouched",
			in:   `/* url(a.png) */a{color:red}`,
			want: `/* url(a.png) */a{color:red}`,
		},
		{
			name: "style attribute declaration list",
			in:   `background:url(a.png);color:red`,
			want: `background:url("U:a.png");color:red`,
			urls: []string{"a.png"},
		},
		{
			name: "custom property",
			in:   `:root{--bg:url(a.png)}`,
			want: `:root{--bg:url("U:a.png")}`,
			urls: []string{"a.png"},
		},
		{
			name: "replacement escaped",
			in:   `a{background:url(q.png)}`,
			want: `a{background:url("U:q.png")}`,
			urls: []string{"q.png"},
		},
		{
			name: "unterminated url at end",
			in:   `a{background:url(a.png`,
			want: `a{background:url("U:a.png")`,
			urls: []string{"a.png"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			got := string(Rewrite([]byte(tc.in), r))
			if got != tc.want {
				t.Errorf("Rewrite = %q\n           want %q", got, tc.want)
			}
			if strings.Join(r.urls, "|") != strings.Join(tc.urls, "|") {
				t.Errorf("urls seen = %q, want %q", r.urls, tc.urls)
			}
			if len(r.imports) != 0 {
				t.Errorf("unexpected imports %q", r.imports)
			}
		})
	}
}

func TestRewriteImports(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		imports []string
		urls    []string
	}{
		{
			name:    "string",
			in:      `@import "base.css";a{}`,
			want:    `@import url("I:base.css");a{}`,
			imports: []string{"base.css"},
		},
		{
			name:    "url function",
			in:      `@import url(base.css);`,
			want:    `@import url("I:base.css");`,
			imports: []string{"base.css"},
		},
		{
			name:    "url with quoted string",
			in:      `@import url("base.css") screen and (min-width: 1px);`,
			want:    `@import url("I:base.css") screen and (min-width: 1px);`,
			imports: []string{"base.css"},
		},
		{
			name:    "layer and supports preserved",
			in:      `@import "a.css" layer(base) supports(display: grid) print;`,
			want:    `@import url("I:a.css") layer(base) supports(display: grid) print;`,
			imports: []string{"a.css"},
		},
		{
			name:    "upper case at-keyword",
			in:      `@IMPORT 'a.css';`,
			want:    `@IMPORT url("I:a.css");`,
			imports: []string{"a.css"},
		},
		{
			name:    "comment between",
			in:      `@import /* c */ "a.css";`,
			want:    `@import /* c */ url("I:a.css");`,
			imports: []string{"a.css"},
		},
		{
			name:    "two imports then url",
			in:      `@import "a.css";@import "b.css";x{background:url(c.png)}`,
			want:    `@import url("I:a.css");@import url("I:b.css");x{background:url("U:c.png")}`,
			imports: []string{"a.css", "b.css"},
			urls:    []string{"c.png"},
		},
		{
			name: "other at-rule strings untouched",
			in:   `@charset "utf-8";@namespace svg url(http://www.w3.org/2000/svg);`,
			want: `@charset "utf-8";@namespace svg url("U:http://www.w3.org/2000/svg");`,
			urls: []string{"http://www.w3.org/2000/svg"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{}
			got := string(Rewrite([]byte(tc.in), r))
			if got != tc.want {
				t.Errorf("Rewrite = %q\n           want %q", got, tc.want)
			}
			if strings.Join(r.imports, "|") != strings.Join(tc.imports, "|") {
				t.Errorf("imports seen = %q, want %q", r.imports, tc.imports)
			}
			if strings.Join(r.urls, "|") != strings.Join(tc.urls, "|") {
				t.Errorf("urls seen = %q, want %q", r.urls, tc.urls)
			}
		})
	}
}

func TestRewriteSkipKeepsOriginalToken(t *testing.T) {
	r := &recorder{skip: map[string]bool{"keep.png": true, "keep.css": true}}
	in := `@import 'keep.css' screen;a{background:url( keep.png ),url(go.png)}`
	want := `@import 'keep.css' screen;a{background:url( keep.png ),url("U:go.png")}`
	if got := string(Rewrite([]byte(in), r)); got != want {
		t.Errorf("Rewrite = %q, want %q", got, want)
	}
}

func TestUnquoteAndEscape(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{`"abc"`, "abc"},
		{`'abc'`, "abc"},
		{`"a\"b"`, `a"b`},
		{`"a\\b"`, `a\b`},
		{"\"a\\\nb\"", "ab"},    // line continuation
		{"\"a\\\r\nb\"", "ab"},  // CRLF continuation
		{`"\41 b"`, "Ab"},       // hex escape consumes one space
		{`"\000041"`, "A"},      // six digits
		{`"\41b"`, "\u041b"},    // greedy hex
		{`"\0"`, "\uFFFD"},      // null → replacement
		{`"\110000"`, "\uFFFD"}, // out of range
		{`"\d800"`, "\uFFFD"},   // surrogate
		{`"\日"`, "日"},           // escaped multibyte char
		{`"unterminated`, "unterminated"},
		{`"trailing\`, "trailing"},
		{`plain`, "plain"},
	}
	for _, tc := range tests {
		if got := Unquote(tc.in); got != tc.want {
			t.Errorf("Unquote(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	esc := []struct{ in, want string }{
		{"plain", "plain"},
		{`a"b`, `a\"b`},
		{`a\b`, `a\\b`},
		{"a\nb", `a\a b`},
		{"日本", "日本"},
	}
	for _, tc := range esc {
		if got := Escape(tc.in); got != tc.want {
			t.Errorf("Escape(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestDecode(t *testing.T) {
	const jp = "a::after{content:\"日本語\"}"
	sjis, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(jp))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		data      []byte
		fallbacks []string
		want      string
		enc       string
	}{
		{"utf-8 plain", []byte(jp), nil, jp, "utf-8"},
		{"charset rule", append([]byte(`@charset "shift_jis";`), sjis...), nil, jp, "shift_jis"},
		{"charset rule stripped even for utf-8", []byte(`@charset "utf-8";` + jp), nil, jp, "utf-8"},
		{"fallback label", sjis, []string{"", "Shift_JIS"}, jp, "shift_jis"},
		{"bom beats charset rule", append([]byte{0xEF, 0xBB, 0xBF}, []byte(`@charset "shift_jis";`+jp)...), nil, jp, "utf-8"},
		{"charset rule beats fallback", append([]byte(`@charset "shift_jis";`), sjis...), []string{"utf-8"}, jp, "shift_jis"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, enc := Decode(tc.data, tc.fallbacks...)
			if got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if enc != tc.enc {
				t.Errorf("encoding = %q, want %q", enc, tc.enc)
			}
		})
	}
}
