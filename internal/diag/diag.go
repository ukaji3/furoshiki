// Package diag collects the non-fatal diagnostics (warnings and informational
// notes) that are produced while a site is bundled.
//
// Bundling never aborts because of a single bad reference: the reference is
// left as it was, a Diagnostic is recorded, and processing continues. The CLI
// prints the collected diagnostics afterwards and, in --strict mode, treats
// any Warning as a failure.
package diag

import (
	"fmt"
	"strings"
	"sync"
)

// Level classifies a Diagnostic.
type Level int

const (
	// Info reports a decision the bundler made that the user may want to
	// know about, but that does not indicate a problem with the input (for
	// example, a file that was skipped because of an --exclude pattern).
	Info Level = iota
	// Warning reports a problem with the input that the bundler worked
	// around (for example, a reference to a file that does not exist).
	Warning
)

// String returns the lower-case name of the level.
func (l Level) String() string {
	switch l {
	case Info:
		return "info"
	case Warning:
		return "warning"
	default:
		return fmt.Sprintf("Level(%d)", int(l))
	}
}

// Diagnostic is a single message about the input.
type Diagnostic struct {
	Level Level
	// Page is the site-relative path of the document (HTML or CSS) that was
	// being processed when the diagnostic arose; empty when not applicable.
	Page string
	// Ref is the reference (URL as written in the document) that the
	// diagnostic is about; empty when not applicable.
	Ref string
	// Message is the human-readable explanation.
	Message string
}

// String formats the diagnostic as `level: page: "ref": message`, omitting
// the page and reference parts when they are empty.
func (d Diagnostic) String() string {
	var b strings.Builder
	b.WriteString(d.Level.String())
	b.WriteString(": ")
	if d.Page != "" {
		b.WriteString(d.Page)
		b.WriteString(": ")
	}
	if d.Ref != "" {
		fmt.Fprintf(&b, "%q: ", d.Ref)
	}
	b.WriteString(d.Message)
	return b.String()
}

// Collector accumulates diagnostics in the order they are reported.
//
// The zero value is ready to use. Methods on a nil *Collector are no-ops, so
// callers that do not care about diagnostics may pass nil.
type Collector struct {
	mu    sync.Mutex
	items []Diagnostic
}

// Add records d.
func (c *Collector) Add(d Diagnostic) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.items = append(c.items, d)
}

// Warnf records a Warning about ref in page. Either may be empty.
func (c *Collector) Warnf(page, ref, format string, args ...any) {
	c.Add(Diagnostic{Level: Warning, Page: page, Ref: ref, Message: fmt.Sprintf(format, args...)})
}

// Infof records an Info note about ref in page. Either may be empty.
func (c *Collector) Infof(page, ref, format string, args ...any) {
	c.Add(Diagnostic{Level: Info, Page: page, Ref: ref, Message: fmt.Sprintf(format, args...)})
}

// Items returns a copy of the recorded diagnostics in insertion order.
func (c *Collector) Items() []Diagnostic {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Diagnostic, len(c.items))
	copy(out, c.items)
	return out
}

// Count returns the number of recorded diagnostics at the given level.
func (c *Collector) Count(level Level) int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, d := range c.items {
		if d.Level == level {
			n++
		}
	}
	return n
}
