// Package store deduplicates the resources (images, fonts, scripts, style
// sheets, downloads, ...) that a bundle embeds, and defines the placeholder
// syntax by which bundled documents refer to them.
//
// A document never contains a resource's bytes directly. Wherever a URL
// pointed at a bundleable file, the bundler writes a placeholder such as
//
//	furoshiki-res:3f9a1c2b7e6d5041
//
// The key after the prefix identifies a Resource. The bundle's runtime
// replaces every placeholder with a data: URL when a page is displayed, so a
// file that is referenced from many pages (or many style sheets) is stored
// exactly once.
//
// Resources come in two flavours. Binary resources are pre-encoded to base64
// data: URLs at bundle time. Text resources (style sheets, scripts) are kept
// as UTF-8 text because their contents may themselves contain placeholders;
// the runtime substitutes those recursively before building the data: URL.
package store

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"regexp"
	"strings"
)

// Prefix introduces a placeholder; it is followed by the resource key.
const Prefix = "furoshiki-res:"

// Placeholder matches placeholders in text. Submatch 1 is the resource key.
// The pattern is mirrored by the runtime script embedded in every bundle.
var Placeholder = regexp.MustCompile(`furoshiki-res:([0-9a-f]{16,64})`)

// keyLen is the number of hexadecimal digits used for a key before a prefix
// collision forces a longer one.
const keyLen = 16

// Resource is a deduplicated resource.
type Resource struct {
	// Key identifies the resource in placeholders and in the bundle payload.
	// It is a prefix of the hexadecimal SHA-256 of the resource's media
	// type and contents, so identical inputs always yield identical keys.
	Key string
	// MIME is the media type. It may carry parameters
	// (for example "text/javascript;charset=utf-8").
	MIME string
	// Data holds the contents: raw bytes for a binary resource, UTF-8 text
	// for a text resource.
	Data []byte
	// Text marks a text resource (see the package documentation).
	Text bool
	// Rel is the site-relative path under which the resource was first
	// seen. It is informational only; deduplication ignores it.
	Rel string
}

// Placeholder returns the token to write into documents in place of a URL
// that referred to the resource.
func (r *Resource) Placeholder() string { return Prefix + r.Key }

// DataURL encodes the resource as a data: URL. Placeholders inside a text
// resource are not substituted; use Store.Expand for a fully resolved URL.
func (r *Resource) DataURL() string {
	if r.Text {
		return textDataURL(r.MIME, r.Data)
	}
	return "data:" + r.MIME + ";base64," + base64.StdEncoding.EncodeToString(r.Data)
}

func textDataURL(mime string, text []byte) string {
	return "data:" + mime + ";charset=utf-8," + PercentEncode(text)
}

// Store deduplicates resources by media type and content.
type Store struct {
	byHash map[[sha256.Size]byte]*Resource
	byKey  map[string]*Resource
	list   []*Resource
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		byHash: make(map[[sha256.Size]byte]*Resource),
		byKey:  make(map[string]*Resource),
	}
}

// Add registers a resource and returns it. Adding the same media type,
// contents and text flag again returns the already registered Resource; the
// Rel of the first registration is kept.
func (s *Store) Add(rel, mime string, data []byte, text bool) *Resource {
	h := sha256.New()
	h.Write([]byte(mime))
	h.Write([]byte{0})
	if text {
		h.Write([]byte{1})
	} else {
		h.Write([]byte{0})
	}
	h.Write(data)
	var sum [sha256.Size]byte
	copy(sum[:], h.Sum(nil))

	if r, ok := s.byHash[sum]; ok {
		return r
	}
	full := hex.EncodeToString(sum[:])
	key := full
	for n := keyLen; n < len(full); n += 8 {
		if _, taken := s.byKey[full[:n]]; !taken {
			key = full[:n]
			break
		}
	}
	r := &Resource{Key: key, MIME: mime, Data: data, Text: text, Rel: rel}
	s.byHash[sum] = r
	s.byKey[key] = r
	s.list = append(s.list, r)
	return r
}

// Get returns the resource with the given key.
func (s *Store) Get(key string) (*Resource, bool) {
	r, ok := s.byKey[key]
	return r, ok
}

// Resources returns all resources in the order they were first added.
func (s *Store) Resources() []*Resource {
	out := make([]*Resource, len(s.list))
	copy(out, s.list)
	return out
}

// Len returns the number of distinct resources.
func (s *Store) Len() int { return len(s.list) }

// Size returns the total number of content bytes over all resources.
func (s *Store) Size() int64 {
	var n int64
	for _, r := range s.list {
		n += int64(len(r.Data))
	}
	return n
}

// Expand replaces every placeholder in text with the corresponding data:
// URL, recursively expanding the contents of text resources. Placeholders
// with unknown keys, and placeholders that would form a cycle, are left
// untouched. Expand mirrors the substitution performed by the bundle's
// runtime and exists for tests and tooling.
func (s *Store) Expand(text string) string {
	return s.expand(text, make(map[string]bool))
}

func (s *Store) expand(text string, active map[string]bool) string {
	return Placeholder.ReplaceAllStringFunc(text, func(m string) string {
		key := m[len(Prefix):]
		r, ok := s.byKey[key]
		if !ok || active[key] {
			return m
		}
		if !r.Text {
			return r.DataURL()
		}
		active[key] = true
		inner := s.expand(string(r.Data), active)
		delete(active, key)
		return textDataURL(r.MIME, []byte(inner))
	})
}

// PercentEncode encodes b exactly like JavaScript's encodeURIComponent:
// every byte except ASCII letters, digits and - _ . ! ~ * ' ( ) becomes a
// %XX escape. The runtime uses encodeURIComponent to build data: URLs for
// text resources, so the two encoders must agree.
func PercentEncode(b []byte) string {
	const hexDigits = "0123456789ABCDEF"
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/4)
	for _, c := range b {
		switch {
		case 'A' <= c && c <= 'Z', 'a' <= c && c <= 'z', '0' <= c && c <= '9':
			sb.WriteByte(c)
		case c == '-', c == '_', c == '.', c == '!', c == '~', c == '*', c == '\'', c == '(', c == ')':
			sb.WriteByte(c)
		default:
			sb.WriteByte('%')
			sb.WriteByte(hexDigits[c>>4])
			sb.WriteByte(hexDigits[c&0x0f])
		}
	}
	return sb.String()
}
