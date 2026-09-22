// Package shell produces the top-level document of a bundle: a small HTML
// "shell" that embeds the runtime script, the style sheet, and the payload
// (all pages and resources as JSON), and that displays the requested page in
// an <iframe srcdoc> driven by the URL fragment.
//
// The runtime assets live next to this file (shell.html.tmpl, shell.css,
// shell.js, shim.js) and are compiled into the binary with embed.
package shell

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"strings"
	"text/template"
)

//go:embed shell.html.tmpl shell.css shell.js shim.js
var assets embed.FS

// PayloadVersion is the format version written to Payload.Version. The
// runtime refuses payloads with another version.
const PayloadVersion = 1

// Page is one bundled HTML document.
type Page struct {
	// Title is the page's <title> text, used for the shell's document.title
	// and the frame's accessible name.
	Title string `json:"title"`
	// HTML is the transformed document with resource placeholders.
	HTML string `json:"html"`
}

// Resource is a bundled file. Exactly one representation is used: a binary
// resource carries a complete data: URL; a text resource carries its media
// type and UTF-8 text, which may contain placeholders that the runtime
// expands before building the data: URL.
type Resource struct {
	DataURL string
	MIME    string
	Text    string
	IsText  bool
}

// MarshalJSON encodes a binary resource as a JSON string and a text resource
// as {"mime": ..., "text": ...}.
func (r Resource) MarshalJSON() ([]byte, error) {
	if r.IsText {
		return json.Marshal(struct {
			MIME string `json:"mime"`
			Text string `json:"text"`
		}{r.MIME, r.Text})
	}
	return json.Marshal(r.DataURL)
}

// UnmarshalJSON accepts both representations.
func (r *Resource) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*r = Resource{DataURL: s}
		return nil
	}
	var t struct {
		MIME string `json:"mime"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return err
	}
	*r = Resource{MIME: t.MIME, Text: t.Text, IsText: true}
	return nil
}

// Payload is everything the runtime needs.
type Payload struct {
	Version   int                 `json:"v"`
	Generator string              `json:"generator"`
	Entry     string              `json:"entry"`
	Pages     map[string]Page     `json:"pages"`
	Resources map[string]Resource `json:"resources"`
}

// Input is what Render needs besides the payload.
type Input struct {
	// Title becomes the shell's initial <title>; the runtime replaces it
	// with the displayed page's title.
	Title string
	// Lang is the lang attribute of the shell's <html>; empty omits it.
	Lang string
	// Generator is written to <meta name=generator> and to the payload,
	// for example "furoshiki v1.2.3".
	Generator string
	Payload   *Payload
}

// Shim returns the script that is injected into every bundled page.
func Shim() string {
	return mustAsset("shim.js")
}

// EncodePayload serialises p as JSON that is safe to place inside a
// <script> element: the sequences "</" and "<!--" cannot occur in the
// output, and neither can U+2028 or U+2029.
func EncodePayload(p *Payload) ([]byte, error) {
	if p == nil {
		return nil, errors.New("shell: nil payload")
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(p); err != nil {
		return nil, fmt.Errorf("shell: encode payload: %w", err)
	}
	out := bytes.TrimRight(buf.Bytes(), "\n")
	// Both replacements produce equivalent JSON: "\/" is the escaped form
	// of "/", and "\u003c" of "<".
	out = bytes.ReplaceAll(out, []byte("</"), []byte(`<\/`))
	out = bytes.ReplaceAll(out, []byte("<!--"), []byte(`\u003c!--`))
	return out, nil
}

var tmpl = template.Must(template.New("shell").Parse(mustAsset("shell.html.tmpl")))

// Render writes the complete shell document for in to w.
func Render(w io.Writer, in Input) error {
	if in.Payload == nil {
		return errors.New("shell: nil payload")
	}
	p := *in.Payload
	p.Version = PayloadVersion
	if p.Generator == "" {
		p.Generator = in.Generator
	}
	payload, err := EncodePayload(&p)
	if err != nil {
		return err
	}
	js := mustAsset("shell.js")
	css := mustAsset("shell.css")
	if err := checkAsset("shell.js", js, "</script"); err != nil {
		return err
	}
	if err := checkAsset("shell.css", css, "</style"); err != nil {
		return err
	}
	data := struct {
		Title, Lang, Generator string
		CSS, JS, Payload       string
	}{
		Title:     html.EscapeString(in.Title),
		Lang:      html.EscapeString(in.Lang),
		Generator: html.EscapeString(in.Generator),
		CSS:       strings.TrimRight(css, "\n"),
		JS:        strings.TrimRight(js, "\n"),
		Payload:   string(payload),
	}
	return tmpl.Execute(w, data)
}

func checkAsset(name, content, forbidden string) error {
	if strings.Contains(strings.ToLower(content), forbidden) {
		return fmt.Errorf("shell: embedded asset %s contains %q", name, forbidden)
	}
	return nil
}

func mustAsset(name string) string {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic("shell: missing embedded asset " + name + ": " + err.Error())
	}
	return string(b)
}
