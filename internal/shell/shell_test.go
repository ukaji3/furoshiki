package shell

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

func samplePayload() *Payload {
	return &Payload{
		Entry: "index.html",
		Pages: map[string]Page{
			"index.html": {Title: "Home </script> <!-- x --> \u2028", HTML: `<!DOCTYPE html><html><head><script>if (a </script><!-- b) {}</script></head><body>日本語 &amp; "quotes" 'single' \ backslash</body></html>`},
			"a b.html":   {Title: "", HTML: "<p>x</p>"},
		},
		Resources: map[string]Resource{
			"0123456789abcdef": {DataURL: "data:image/png;base64,iVBORw=="},
			"fedcba9876543210": {IsText: true, MIME: "text/css", Text: "a{background:url(\"furoshiki-res:0123456789abcdef\")} /* </style> */"},
		},
	}
}

var payloadRE = regexp.MustCompile(`(?s)<script type="application/json" id="furoshiki-payload">(.*?)</script>`)

func TestEncodePayloadIsScriptSafe(t *testing.T) {
	out, err := EncodePayload(samplePayload())
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	for _, bad := range []string{"</", "<!--", "\u2028", "\u2029", "\n"} {
		if strings.Contains(s, bad) {
			t.Errorf("encoded payload contains %q", bad)
		}
	}
	var back Payload
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("round trip: %v\n%s", err, s)
	}
	want := samplePayload()
	if back.Entry != want.Entry || len(back.Pages) != len(want.Pages) || len(back.Resources) != len(want.Resources) {
		t.Fatalf("round trip changed shape: %+v", back)
	}
	for k, p := range want.Pages {
		if back.Pages[k] != p {
			t.Errorf("page %q = %+v, want %+v", k, back.Pages[k], p)
		}
	}
	for k, r := range want.Resources {
		if back.Resources[k] != r {
			t.Errorf("resource %q = %+v, want %+v", k, back.Resources[k], r)
		}
	}
}

func TestEncodePayloadNil(t *testing.T) {
	if _, err := EncodePayload(nil); err == nil {
		t.Error("EncodePayload(nil) should fail")
	}
	if err := Render(&bytes.Buffer{}, Input{}); err == nil {
		t.Error("Render without payload should fail")
	}
}

func TestResourceJSON(t *testing.T) {
	bin, _ := json.Marshal(Resource{DataURL: "data:x,y"})
	if string(bin) != `"data:x,y"` {
		t.Errorf("binary resource = %s", bin)
	}
	txt, _ := json.Marshal(Resource{IsText: true, MIME: "text/css", Text: "a{}"})
	if string(txt) != `{"mime":"text/css","text":"a{}"}` {
		t.Errorf("text resource = %s", txt)
	}
	var r Resource
	if err := json.Unmarshal([]byte(`[1]`), &r); err == nil {
		t.Error("unmarshal of an array should fail")
	}
}

func TestRender(t *testing.T) {
	var buf bytes.Buffer
	in := Input{
		Title:     `Site <"&'> title`,
		Lang:      `ja"`,
		Generator: "furoshiki vTEST <x>",
		Payload:   samplePayload(),
	}
	if err := Render(&buf, in); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"<!DOCTYPE html>\n<html lang=\"ja&#34;\">",
		`<meta charset="utf-8">`,
		`<meta name="generator" content="furoshiki vTEST &lt;x&gt;">`,
		`<title>Site &lt;&#34;&amp;&#39;&gt; title</title>`,
		`<main id="furoshiki-view"`,
		`<noscript>`,
		`window.__furoshiki = {`,
		`#furoshiki-view > iframe {`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}

	m := payloadRE.FindStringSubmatch(out)
	if m == nil {
		t.Fatal("payload script element not found")
	}
	var p Payload
	if err := json.Unmarshal([]byte(m[1]), &p); err != nil {
		t.Fatalf("payload is not valid JSON: %v", err)
	}
	if p.Version != PayloadVersion {
		t.Errorf("payload version = %d, want %d", p.Version, PayloadVersion)
	}
	if p.Generator != "furoshiki vTEST <x>" {
		t.Errorf("payload generator = %q", p.Generator)
	}
	if p.Pages["index.html"] != samplePayload().Pages["index.html"] {
		t.Errorf("page round trip failed: %+v", p.Pages["index.html"])
	}

	// The document must contain exactly the expected number of script
	// terminators: one for the payload, one for the runtime. Anything else
	// means an asset or the payload broke out of its element.
	if n := strings.Count(strings.ToLower(out), "</script>"); n != 2 {
		t.Errorf("found %d </script> terminators, want 2", n)
	}
	// The style sheet must be embedded verbatim and closed exactly once.
	css := strings.TrimRight(mustAsset("shell.css"), "\n")
	if !strings.Contains(out, "<style>\n"+css+"\n</style>") {
		t.Error("shell.css is not embedded verbatim inside <style>")
	}
	if strings.Contains(strings.ToLower(css), "</style") {
		t.Error("shell.css must not contain </style")
	}
}

func TestRenderOmitsEmptyLang(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, Input{Payload: samplePayload()}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "<html>\n") {
		t.Errorf("expected bare <html>, got %q", buf.String()[:80])
	}
}

func TestRenderIsDeterministic(t *testing.T) {
	var a, b bytes.Buffer
	in := Input{Title: "t", Generator: "g", Payload: samplePayload()}
	if err := Render(&a, in); err != nil {
		t.Fatal(err)
	}
	if err := Render(&b, in); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two renders of the same input differ")
	}
}

func TestShimAsset(t *testing.T) {
	shim := Shim()
	if !strings.Contains(shim, "data-furoshiki-page") || !strings.Contains(shim, "navigate") {
		t.Error("shim does not look like the page shim")
	}
	if strings.Contains(strings.ToLower(shim), "</script") {
		t.Error("shim must not contain </script")
	}
}

func TestAssetsAgreeOnPlaceholderSyntax(t *testing.T) {
	// The runtime must recognise exactly the placeholders the Go side emits.
	js := mustAsset("shell.js")
	if !strings.Contains(js, `'furoshiki-res:([0-9a-f]{16,64})'`) {
		t.Error("shell.js placeholder pattern differs from package store")
	}
	if !strings.Contains(js, "payload.v !== 1") {
		t.Error("shell.js payload version check differs from PayloadVersion")
	}
}
