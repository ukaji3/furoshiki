package diag

import (
	"testing"
)

func TestDiagnosticString(t *testing.T) {
	tests := []struct {
		name string
		d    Diagnostic
		want string
	}{
		{
			name: "full",
			d:    Diagnostic{Level: Warning, Page: "index.html", Ref: "missing.png", Message: "file not found"},
			want: `warning: index.html: "missing.png": file not found`,
		},
		{
			name: "no ref",
			d:    Diagnostic{Level: Info, Page: "a.html", Message: "decoded from shift_jis"},
			want: "info: a.html: decoded from shift_jis",
		},
		{
			name: "no page",
			d:    Diagnostic{Level: Warning, Message: "something"},
			want: "warning: something",
		},
		{
			name: "unknown level",
			d:    Diagnostic{Level: Level(7), Message: "x"},
			want: "Level(7): x",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.d.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCollector(t *testing.T) {
	var c Collector
	c.Warnf("p.html", "x.css", "not found: %d", 1)
	c.Infof("p.html", "", "note")
	c.Warnf("", "", "bare")

	items := c.Items()
	if len(items) != 3 {
		t.Fatalf("Items() len = %d, want 3", len(items))
	}
	if items[0].Level != Warning || items[0].Page != "p.html" || items[0].Ref != "x.css" || items[0].Message != "not found: 1" {
		t.Errorf("items[0] = %+v", items[0])
	}
	if items[1].Level != Info || items[1].Message != "note" {
		t.Errorf("items[1] = %+v", items[1])
	}
	if got := c.Count(Warning); got != 2 {
		t.Errorf("Count(Warning) = %d, want 2", got)
	}
	if got := c.Count(Info); got != 1 {
		t.Errorf("Count(Info) = %d, want 1", got)
	}

	// Items returns a copy.
	items[0].Message = "mutated"
	if c.Items()[0].Message == "mutated" {
		t.Error("Items() returned internal slice")
	}
}

func TestNilCollector(t *testing.T) {
	var c *Collector
	c.Warnf("p", "r", "ignored")
	c.Infof("p", "r", "ignored")
	c.Add(Diagnostic{})
	if c.Items() != nil {
		t.Error("nil Collector Items() should be nil")
	}
	if c.Count(Warning) != 0 {
		t.Error("nil Collector Count() should be 0")
	}
}
