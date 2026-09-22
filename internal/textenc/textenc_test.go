package textenc

import (
	"testing"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/unicode"
)

func encode(t *testing.T, enc encoding.Encoding, s string) []byte {
	t.Helper()
	b, err := enc.NewEncoder().Bytes([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestLookup(t *testing.T) {
	tests := []struct {
		label string
		name  string
		ok    bool
	}{
		{"utf-8", "utf-8", true},
		{"UTF8", "utf-8", true},
		{" Shift_JIS ", "shift_jis", true},
		{"sjis", "shift_jis", true},
		{"windows-31j", "shift_jis", true},
		{"euc-jp", "euc-jp", true},
		{"iso-8859-1", "windows-1252", true},
		{"latin1", "windows-1252", true},
		{"utf-16", "utf-8", true},
		{"utf-16le", "utf-8", true},
		{"x-user-defined", "windows-1252", true},
		{"", "", false},
		{"klingon", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			enc, name, ok := Lookup(tc.label)
			if ok != tc.ok || name != tc.name {
				t.Errorf("Lookup(%q) = (%q, %v), want (%q, %v)", tc.label, name, ok, tc.name, tc.ok)
			}
			if ok && enc == nil {
				t.Error("ok but nil encoding")
			}
		})
	}
}

func TestBOM(t *testing.T) {
	tests := []struct {
		data []byte
		want string
	}{
		{[]byte{0xEF, 0xBB, 0xBF, 'a'}, "utf-8"},
		{[]byte{0xFE, 0xFF, 0, 'a'}, "utf-16be"},
		{[]byte{0xFF, 0xFE, 'a', 0}, "utf-16le"},
		{[]byte("plain"), ""},
		{nil, ""},
	}
	for _, tc := range tests {
		if got := BOM(tc.data); got != tc.want {
			t.Errorf("BOM(%v) = %q, want %q", tc.data, got, tc.want)
		}
	}
}

func TestDecode(t *testing.T) {
	const jp = "日本語のテキスト"
	sjis := encode(t, japanese.ShiftJIS, jp)
	eucjp := encode(t, japanese.EUCJP, jp)
	utf16be := encode(t, unicode.UTF16(unicode.BigEndian, unicode.UseBOM), jp)
	utf16le := encode(t, unicode.UTF16(unicode.LittleEndian, unicode.UseBOM), jp)

	tests := []struct {
		name   string
		data   []byte
		labels []string
		want   string
		enc    string
	}{
		{"utf-8 default", []byte(jp), nil, jp, "utf-8"},
		{"utf-8 bom stripped", append([]byte{0xEF, 0xBB, 0xBF}, jp...), nil, jp, "utf-8"},
		{"shift_jis label", sjis, []string{"Shift_JIS"}, jp, "shift_jis"},
		{"euc-jp label second", eucjp, []string{"", "bogus", "euc-jp"}, jp, "euc-jp"},
		{"utf-16be bom wins over label", utf16be, []string{"shift_jis"}, jp, "utf-16be"},
		{"utf-16le bom", utf16le, nil, jp, "utf-16le"},
		{"utf-16 label treated as utf-8", []byte(jp), []string{"utf-16"}, jp, "utf-8"},
		{"invalid utf-8 replaced", []byte{'a', 0xff, 'b'}, nil, "a\uFFFDb", "utf-8"},
		{"unknown label falls back", []byte("abc"), []string{"klingon"}, "abc", "utf-8"},
		{"empty", nil, nil, "", "utf-8"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, enc := Decode(tc.data, tc.labels...)
			if got != tc.want {
				t.Errorf("text = %q, want %q", got, tc.want)
			}
			if enc != tc.enc {
				t.Errorf("encoding = %q, want %q", enc, tc.enc)
			}
		})
	}
}

func TestSniffHTML(t *testing.T) {
	tests := []struct {
		name string
		html string
		want string
	}{
		{"charset attr", `<!DOCTYPE html><html><head><meta charset="Shift_JIS"><title>x</title>`, "Shift_JIS"},
		{"charset attr unquoted", `<meta charset=euc-jp>`, "euc-jp"},
		{"http-equiv", `<html><head><meta http-equiv="Content-Type" content="text/html; charset=iso-8859-1">`, "iso-8859-1"},
		{"http-equiv case", `<META HTTP-EQUIV="content-type" CONTENT="text/html;charset = 'windows-1252'">`, "windows-1252"},
		{"none", `<html><head><title>x</title></head>`, ""},
		{"in comment ignored", `<!-- <meta charset="euc-jp"> --><meta charset="utf-8">`, "utf-8"},
		{"first wins", `<meta charset="a"><meta charset="b">`, "a"},
		{"beyond 1024 ignored", string(make([]byte, 1100)) + `<meta charset="euc-jp">`, ""},
		{"other meta", `<meta name="viewport" content="width=device-width">`, ""},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SniffHTML([]byte(tc.html)); got != tc.want {
				t.Errorf("SniffHTML = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSniffCSS(t *testing.T) {
	tests := []struct {
		css  string
		want string
	}{
		{`@charset "shift_jis";body{}`, "shift_jis"},
		{`@charset "UTF-8";`, "UTF-8"},
		{` @charset "utf-8";`, ""},
		{`@charset 'utf-8';`, ""},
		{`body{}`, ""},
	}
	for _, tc := range tests {
		if got := SniffCSS([]byte(tc.css)); got != tc.want {
			t.Errorf("SniffCSS(%q) = %q, want %q", tc.css, got, tc.want)
		}
	}
}

func TestStripCSSCharset(t *testing.T) {
	tests := []struct {
		css  string
		want string
	}{
		{`@charset "shift_jis";body{}`, "body{}"},
		{"@charset \"utf-8\";\n\nbody{}", "body{}"},
		{"body{}", "body{}"},
		{`a{} @charset "x";`, `a{} @charset "x";`},
	}
	for _, tc := range tests {
		if got := StripCSSCharset(tc.css); got != tc.want {
			t.Errorf("StripCSSCharset(%q) = %q, want %q", tc.css, got, tc.want)
		}
	}
}
