package store

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestAddDeduplicates(t *testing.T) {
	s := New()
	a := s.Add("img/logo.png", "image/png", []byte("PNGDATA"), false)
	b := s.Add("other/logo.png", "image/png", []byte("PNGDATA"), false)
	if a != b {
		t.Fatal("identical content should return the same resource")
	}
	if a.Rel != "img/logo.png" {
		t.Errorf("Rel = %q, want first-seen path", a.Rel)
	}
	if s.Len() != 1 {
		t.Errorf("Len = %d, want 1", s.Len())
	}

	c := s.Add("x.bin", "application/octet-stream", []byte("PNGDATA"), false)
	if c == a {
		t.Error("different media type should be a different resource")
	}
	d := s.Add("x.css", "text/css", []byte("PNGDATA"), true)
	e := s.Add("x.css", "text/css", []byte("PNGDATA"), false)
	if d == e {
		t.Error("text flag should distinguish resources")
	}
	if s.Len() != 4 {
		t.Errorf("Len = %d, want 4", s.Len())
	}
	if got := s.Size(); got != 4*int64(len("PNGDATA")) {
		t.Errorf("Size = %d", got)
	}
	res := s.Resources()
	if len(res) != 4 || res[0] != a || res[1] != c || res[2] != d || res[3] != e {
		t.Error("Resources() should preserve insertion order")
	}
}

func TestKeyFormat(t *testing.T) {
	s := New()
	r := s.Add("a.css", "text/css", []byte("body{}"), true)
	if len(r.Key) != keyLen {
		t.Errorf("Key length = %d, want %d", len(r.Key), keyLen)
	}
	sum := sha256.Sum256(append([]byte("text/css\x00\x01"), []byte("body{}")...))
	if want := hex.EncodeToString(sum[:])[:keyLen]; r.Key != want {
		t.Errorf("Key = %q, want %q", r.Key, want)
	}
	ph := r.Placeholder()
	if !strings.HasPrefix(ph, Prefix) {
		t.Errorf("Placeholder %q lacks prefix", ph)
	}
	m := Placeholder.FindStringSubmatch("x" + ph + ")")
	if m == nil || m[1] != r.Key {
		t.Errorf("Placeholder regexp did not match %q: %v", ph, m)
	}
	got, ok := s.Get(r.Key)
	if !ok || got != r {
		t.Error("Get by key failed")
	}
	if _, ok := s.Get("nope"); ok {
		t.Error("Get of unknown key should fail")
	}
}

func TestKeyIsDeterministic(t *testing.T) {
	a := New().Add("a", "image/png", []byte("same"), false)
	b := New().Add("b", "image/png", []byte("same"), false)
	if a.Key != b.Key {
		t.Errorf("keys differ across stores: %q vs %q", a.Key, b.Key)
	}
}

func TestKeyCollisionExtends(t *testing.T) {
	s := New()
	data := []byte("collide")
	sum := sha256.Sum256(append([]byte("image/png\x00\x00"), data...))
	full := hex.EncodeToString(sum[:])
	// Occupy the 16- and 24-digit prefixes with unrelated resources.
	s.byKey[full[:16]] = &Resource{Key: full[:16]}
	s.byKey[full[:24]] = &Resource{Key: full[:24]}

	r := s.Add("a.png", "image/png", data, false)
	if r.Key != full[:32] {
		t.Errorf("Key = %q, want 32-digit prefix %q", r.Key, full[:32])
	}
	if !Placeholder.MatchString(r.Placeholder()) {
		t.Error("extended key should still match the placeholder regexp")
	}

	// Occupying every prefix falls back to the full digest.
	s2 := New()
	for n := 16; n < 64; n += 8 {
		s2.byKey[full[:n]] = &Resource{}
	}
	if r2 := s2.Add("a.png", "image/png", data, false); r2.Key != full {
		t.Errorf("Key = %q, want full digest", r2.Key)
	}
}

func TestDataURL(t *testing.T) {
	s := New()
	bin := s.Add("a.png", "image/png", []byte{0x89, 'P', 'N', 'G'}, false)
	if got, want := bin.DataURL(), "data:image/png;base64,iVBORw=="; got != want {
		t.Errorf("binary DataURL = %q, want %q", got, want)
	}
	txt := s.Add("a.css", "text/css", []byte("a{color:#fff;}"), true)
	if got, want := txt.DataURL(), "data:text/css;charset=utf-8,a%7Bcolor%3A%23fff%3B%7D"; got != want {
		t.Errorf("text DataURL = %q, want %q", got, want)
	}
}

func TestPercentEncode(t *testing.T) {
	// Expected values are what encodeURIComponent produces in a browser.
	tests := []struct {
		in, want string
	}{
		{"abcXYZ019", "abcXYZ019"},
		{"-_.!~*'()", "-_.!~*'()"},
		{" ", "%20"},
		{"a b+c", "a%20b%2Bc"},
		{"#?&=/", "%23%3F%26%3D%2F"},
		{`"<>`, "%22%3C%3E"},
		{"日本", "%E6%97%A5%E6%9C%AC"},
		{"\n\t", "%0A%09"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := PercentEncode([]byte(tc.in)); got != tc.want {
			t.Errorf("PercentEncode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestExpand(t *testing.T) {
	s := New()
	font := s.Add("f.woff2", "font/woff2", []byte("WOFF"), false)
	css := s.Add("a.css", "text/css", []byte(`@font-face{src:url("`+font.Placeholder()+`")}`), true)
	html := `<link href="` + css.Placeholder() + `"><img src="` + font.Placeholder() + `"><a href="furoshiki-res:0000000000000000">`

	got := s.Expand(html)
	if strings.Contains(got, css.Placeholder()) || strings.Contains(got, font.Placeholder()) {
		t.Errorf("placeholders remain: %s", got)
	}
	if !strings.Contains(got, `href="furoshiki-res:0000000000000000"`) {
		t.Error("unknown placeholder should be left untouched")
	}
	if !strings.Contains(got, "data:font/woff2;base64,V09GRg==") {
		t.Errorf("font data URL missing in %s", got)
	}
	// The style sheet's own data URL must contain the already-expanded font
	// URL, percent-encoded.
	wantInner := PercentEncode([]byte(`@font-face{src:url("data:font/woff2;base64,V09GRg==")}`))
	if !strings.Contains(got, "data:text/css;charset=utf-8,"+wantInner) {
		t.Errorf("css data URL wrong in %s", got)
	}
}

func TestExpandCycle(t *testing.T) {
	s := New()
	// A text resource that refers to itself must not recurse forever.
	self := s.Add("loop.css", "text/css", []byte("@import url(SELF);"), true)
	self.Data = []byte("@import url(" + self.Placeholder() + ");")
	got := s.Expand(self.Placeholder())
	if !strings.HasPrefix(got, "data:text/css;charset=utf-8,") {
		t.Fatalf("got %q", got)
	}
	if !strings.Contains(got, PercentEncode([]byte(self.Placeholder()))) {
		t.Error("inner self-reference should remain a placeholder")
	}
}
