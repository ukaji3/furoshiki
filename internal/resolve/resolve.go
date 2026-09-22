// Package resolve maps the URL references found in a site's documents to
// files under the site root.
//
// The site root plays the role of the URL origin, exactly as if the directory
// were served by a web server from "/": root-relative references such as
// "/css/site.css" resolve against the root, "../" segments that would climb
// above the root are reported as Outside, and a reference to a directory
// resolves to the directory's index.html.
//
// Every reference is classified by Kind so that callers can decide whether to
// bundle it (File), leave it untouched (External, Fragment, Empty), or leave
// it untouched and report a diagnostic (Missing, Outside, Invalid, Excluded).
package resolve

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Kind classifies a resolved reference.
type Kind int

const (
	// Empty is an empty (or whitespace-only) reference.
	Empty Kind = iota
	// Fragment is a same-document reference such as "#section".
	Fragment
	// External is a reference with a scheme or a host (http:, https:,
	// mailto:, data:, javascript:, "//host/path", ...). It is never bundled.
	External
	// Invalid is a reference that cannot be parsed as a URL.
	Invalid
	// Outside is a reference that resolves to a path above the site root.
	Outside
	// Missing is a reference to a path under the site root that does not
	// exist or is not a regular file.
	Missing
	// Excluded is a reference to a path matched by an exclude pattern.
	Excluded
	// File is a reference to an existing regular file under the site root.
	File
)

// String returns the lower-case name of the kind.
func (k Kind) String() string {
	switch k {
	case Empty:
		return "empty"
	case Fragment:
		return "fragment"
	case External:
		return "external"
	case Invalid:
		return "invalid"
	case Outside:
		return "outside"
	case Missing:
		return "missing"
	case Excluded:
		return "excluded"
	case File:
		return "file"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Ref is the result of resolving a reference.
type Ref struct {
	Kind Kind
	// Raw is the reference exactly as it appeared in the document.
	Raw string
	// Rel is the cleaned, slash-separated, site-relative path. It is set for
	// File, Missing and Excluded; for Outside it holds the escaping path
	// (for example "../secret.txt").
	Rel string
	// Abs is the filesystem path of the file. It is set for File, Missing
	// and Excluded.
	Abs string
	// Query is the raw query string without the leading "?".
	Query string
	// Fragment is the (percent-escaped) fragment without the leading "#".
	Fragment string
	// IsHTML reports whether Rel has an HTML file extension.
	IsHTML bool
	// Err holds the underlying error for Invalid and Missing references.
	Err error
}

// Resolver resolves references against a site root.
type Resolver struct {
	root     string
	excludes []string
}

// New returns a Resolver for the directory root. Exclude patterns use the
// syntax of path.Match; see Excluded for how they are applied.
func New(root string, excludes []string) (*Resolver, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return nil, fmt.Errorf("%s: not a directory", root)
	}
	cleaned := make([]string, 0, len(excludes))
	for _, p := range excludes {
		p = strings.TrimPrefix(strings.TrimPrefix(p, "./"), "/")
		if p == "" {
			continue
		}
		if _, err := path.Match(p, ""); err != nil {
			return nil, fmt.Errorf("invalid exclude pattern %q: %w", p, err)
		}
		cleaned = append(cleaned, p)
	}
	return &Resolver{root: abs, excludes: cleaned}, nil
}

// Root returns the absolute path of the site root.
func (r *Resolver) Root() string { return r.root }

// Abs returns the filesystem path of a site-relative path.
func (r *Resolver) Abs(rel string) string {
	return filepath.Join(r.root, filepath.FromSlash(rel))
}

// Resolve resolves the reference raw, found in the document whose base is
// base.
//
// base is the site-relative path of the document ("docs/guide.html"); a
// trailing slash denotes a directory ("docs/") and the empty string denotes
// the site root. Use BaseOf to compute the base of a document that declares
// <base href>.
func (r *Resolver) Resolve(raw, base string) Ref {
	ref := Ref{Raw: raw}
	s := stripURLWhitespace(raw)
	if s == "" {
		ref.Kind = Empty
		return ref
	}
	if s[0] == '#' {
		ref.Kind = Fragment
		ref.Fragment = s[1:]
		return ref
	}
	u, err := url.Parse(s)
	if err != nil {
		ref.Kind = Invalid
		ref.Err = err
		return ref
	}
	if isExternal(u, s) {
		ref.Kind = External
		return ref
	}
	ref.Query = u.RawQuery
	ref.Fragment = u.EscapedFragment()

	rel, escaped := joinPath(base, u.Path)
	if escaped {
		ref.Kind = Outside
		ref.Rel = rel
		return ref
	}
	return r.lookup(ref, rel)
}

// BaseOf computes the effective base of the document at pageRel that declares
// <base href=href>. The returned base can be passed to Resolve. kind is File
// when the base lies inside the site, External when the base is an absolute
// URL (relative references then point outside the site), Outside when it
// climbs above the site root, and Invalid when href cannot be parsed. For
// External, Outside and Invalid the returned base is pageRel itself.
func BaseOf(pageRel, href string) (base string, kind Kind) {
	s := stripURLWhitespace(href)
	if s == "" || s[0] == '#' {
		return pageRel, File
	}
	u, err := url.Parse(s)
	if err != nil {
		return pageRel, Invalid
	}
	if isExternal(u, s) {
		return pageRel, External
	}
	if u.Path == "" {
		return pageRel, File
	}
	rel, escaped := joinPath(pageRel, u.Path)
	if escaped {
		return pageRel, Outside
	}
	// A base that names a directory must keep its trailing slash so that
	// path.Dir(base) is the directory itself.
	if rel != "" && (strings.HasSuffix(u.Path, "/") || strings.HasSuffix(u.Path, "/.") || strings.HasSuffix(u.Path, "/..") || u.Path == "." || u.Path == "..") {
		rel += "/"
	}
	return rel, File
}

// Excluded reports whether the site-relative path rel matches an exclude
// pattern.
//
// A pattern is matched (with path.Match) against the whole path and against
// every leading directory of it; a pattern that contains no slash is also
// matched against every individual path segment. Consequently "drafts"
// excludes everything below drafts/, "*.bak" excludes such files anywhere,
// and "docs/internal/*" excludes that subtree.
func (r *Resolver) Excluded(rel string) bool {
	for _, pat := range r.excludes {
		if matchPattern(pat, rel) {
			return true
		}
	}
	return false
}

func matchPattern(pat, rel string) bool {
	if ok, _ := path.Match(pat, rel); ok {
		return true
	}
	segs := strings.Split(rel, "/")
	if !strings.Contains(pat, "/") {
		for _, s := range segs {
			if ok, _ := path.Match(pat, s); ok {
				return true
			}
		}
		return false
	}
	prefix := ""
	for _, s := range segs[:len(segs)-1] {
		prefix = path.Join(prefix, s)
		if ok, _ := path.Match(pat, prefix); ok {
			return true
		}
	}
	return false
}

func (r *Resolver) lookup(ref Ref, rel string) Ref {
	abs := r.Abs(rel)
	fi, err := os.Stat(abs)
	if err == nil && fi.IsDir() {
		rel = path.Join(rel, "index.html")
		abs = r.Abs(rel)
		fi, err = os.Stat(abs)
	}
	ref.Rel = rel
	ref.Abs = abs
	ref.IsHTML = IsHTMLPath(rel)
	if r.Excluded(rel) {
		ref.Kind = Excluded
		return ref
	}
	if err != nil {
		ref.Kind = Missing
		ref.Err = err
		return ref
	}
	if !fi.Mode().IsRegular() {
		ref.Kind = Missing
		ref.Err = errors.New("not a regular file")
		return ref
	}
	ref.Kind = File
	return ref
}

// isExternal reports whether a parsed reference has a scheme or an authority.
func isExternal(u *url.URL, raw string) bool {
	return u.Scheme != "" || u.Host != "" || u.Opaque != "" || u.User != nil || strings.HasPrefix(raw, "//")
}

// joinPath resolves the URL path p against base and returns the cleaned
// site-relative result. escaped is true when the result climbs above the
// site root; rel then holds the escaping path.
func joinPath(base, p string) (rel string, escaped bool) {
	// Browsers treat backslashes in special-scheme URLs as slashes.
	p = strings.ReplaceAll(p, "\\", "/")
	var joined string
	switch {
	case p == "":
		joined = base // a query-only or empty path refers to the document itself
	case p[0] == '/':
		joined = p[1:]
	default:
		joined = path.Join(path.Dir(base), p)
	}
	rel = path.Clean(joined)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return rel, true
	}
	if rel == "." {
		rel = ""
	}
	return rel, false
}

// stripURLWhitespace removes leading and trailing C0 controls and spaces and
// deletes ASCII tabs and newlines anywhere, as the URL parser does.
func stripURLWhitespace(s string) string {
	s = strings.TrimFunc(s, func(r rune) bool { return r <= ' ' })
	if strings.ContainsAny(s, "\t\n\r") {
		s = strings.Map(func(r rune) rune {
			if r == '\t' || r == '\n' || r == '\r' {
				return -1
			}
			return r
		}, s)
	}
	return s
}

// IsHTMLPath reports whether p has an HTML file extension (.html or .htm,
// case-insensitively).
func IsHTMLPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".html", ".htm":
		return true
	}
	return false
}

// MIMEType returns the media type to use for the file at p with contents
// data. Common web file extensions are looked up in a fixed table so that the
// result does not depend on the host's MIME database; unknown extensions are
// content-sniffed, and application/octet-stream is the last resort.
func MIMEType(p string, data []byte) string {
	if m, ok := mimeByExt[strings.ToLower(path.Ext(p))]; ok {
		return m
	}
	m := http.DetectContentType(data)
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	if m == "" {
		return "application/octet-stream"
	}
	return m
}

var mimeByExt = map[string]string{
	// documents
	".html":        "text/html",
	".htm":         "text/html",
	".xhtml":       "application/xhtml+xml",
	".xml":         "application/xml",
	".txt":         "text/plain",
	".md":          "text/markdown",
	".csv":         "text/csv",
	".vtt":         "text/vtt",
	".pdf":         "application/pdf",
	".json":        "application/json",
	".map":         "application/json",
	".webmanifest": "application/manifest+json",
	// code
	".css":  "text/css",
	".js":   "text/javascript",
	".mjs":  "text/javascript",
	".cjs":  "text/javascript",
	".wasm": "application/wasm",
	// images
	".svg":  "image/svg+xml",
	".png":  "image/png",
	".apng": "image/apng",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".jfif": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".jxl":  "image/jxl",
	".bmp":  "image/bmp",
	".ico":  "image/vnd.microsoft.icon",
	".cur":  "image/vnd.microsoft.icon",
	".tif":  "image/tiff",
	".tiff": "image/tiff",
	// fonts
	".woff":  "font/woff",
	".woff2": "font/woff2",
	".ttf":   "font/ttf",
	".otf":   "font/otf",
	".ttc":   "font/collection",
	".eot":   "application/vnd.ms-fontobject",
	// audio
	".mp3":  "audio/mpeg",
	".m4a":  "audio/mp4",
	".aac":  "audio/aac",
	".wav":  "audio/wav",
	".flac": "audio/flac",
	".ogg":  "audio/ogg",
	".oga":  "audio/ogg",
	".opus": "audio/ogg",
	".weba": "audio/webm",
	".mid":  "audio/midi",
	".midi": "audio/midi",
	// video
	".mp4":  "video/mp4",
	".m4v":  "video/mp4",
	".webm": "video/webm",
	".ogv":  "video/ogg",
	".mov":  "video/quicktime",
	".avi":  "video/x-msvideo",
	".mkv":  "video/x-matroska",
	// archives and office documents
	".zip":  "application/zip",
	".gz":   "application/gzip",
	".tar":  "application/x-tar",
	".7z":   "application/x-7z-compressed",
	".rar":  "application/vnd.rar",
	".doc":  "application/msword",
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	".xls":  "application/vnd.ms-excel",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	".ppt":  "application/vnd.ms-powerpoint",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation",
	".odt":  "application/vnd.oasis.opendocument.text",
	".ods":  "application/vnd.oasis.opendocument.spreadsheet",
	".odp":  "application/vnd.oasis.opendocument.presentation",
	".epub": "application/epub+zip",
	".ics":  "text/calendar",
	".vcf":  "text/vcard",
}
