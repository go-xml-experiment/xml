// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

func drainAll(d *Decoder) error {
	for {
		if _, err := d.ReadToken(); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
	}
}

// within fails the test if f does not finish in limit. The limits are far
// above the linear cost and far below the quadratic one the inputs would
// otherwise trigger, so they are not sensitive to machine speed.
func within(t *testing.T, limit time.Duration, f func()) {
	t.Helper()
	start := time.Now()
	f()
	if el := time.Since(start); el > limit {
		t.Fatalf("took %v, limit %v (super-linear behaviour?)", el, limit)
	}
}

type repeatReader struct{ b byte }

func (r repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b
	}
	return len(p), nil
}

// Nesting is limited by MaxDepth (default 10000) and the parser is iterative,
// so deep documents neither recurse nor exhaust the stack (cf. CVE-2022-28131,
// CVE-2022-30633, CVE-2026-56859 in encoding/xml).
func TestDeepNestingBounded(t *testing.T) {
	deep := []byte(strings.Repeat("<a>", 1_000_000))
	err := drainAll(NewDecoderBytes(deep))
	if err == nil || !strings.Contains(err.Error(), "exceeded max depth") {
		t.Fatalf("default depth limit: %v", err)
	}
	// An explicit large limit still works without recursion in any entry point.
	for name, f := range map[string]func(d *Decoder) error{
		"ReadToken":   drainAll,
		"SkipElement": func(d *Decoder) error { d.ReadToken(); return d.SkipElement() },
		"ReadElement": func(d *Decoder) error { d.ReadToken(); _, err := d.ReadElement(); return err },
	} {
		err := f(NewDecoderBytes(deep, MaxDepth(2_000_000)))
		if err == nil || !strings.Contains(err.Error(), "unexpected EOF") {
			t.Errorf("%s with MaxDepth(2e6): %v", name, err)
		}
	}
	closed := strings.Repeat("<a>", 100_000) + strings.Repeat("</a>", 100_000)
	if err := drainAll(NewDecoderBytes([]byte(closed), MaxDepth(200_000))); err != nil {
		t.Fatalf("balanced deep document: %v", err)
	}
}

// Duplicate-attribute detection must stay linear in the number of attributes.
func TestManyAttributesLinear(t *testing.T) {
	const n = 100_000
	var b strings.Builder
	b.WriteString("<a")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, " k%d=\"v\"", i)
	}
	b.WriteString("/>")
	in := b.String()
	within(t, 2*time.Second, func() {
		if err := drainAll(NewDecoderBytes([]byte(in), MaxAttributes(0))); err != nil {
			t.Fatal(err)
		}
	})
	// Duplicates are still found, early and late, below and above the set threshold.
	for _, c := range []struct{ name, in string }{
		{"small", `<a x="1" y="2" x="3"/>`},
		{"set-late", manyAttrs(60) + ` k59="z"/>`},
		{"set-early", manyAttrs(60) + ` k0="z"/>`},
		{"set-across-threshold", manyAttrs(attrSetMin-1) + ` k5="z"/>`},
		{"set-new-then-dup", manyAttrs(40) + ` n1="1" n1="2"/>`},
	} {
		err := drainAll(NewDecoderBytes([]byte(c.in)))
		if err == nil || !strings.Contains(err.Error(), "duplicate attribute") {
			t.Errorf("%s: want duplicate attribute error, got %v", c.name, err)
		}
	}
	if err := drainAll(NewDecoderBytes([]byte(manyAttrs(80) + `/>`))); err != nil {
		t.Errorf("distinct attributes rejected: %v", err)
	}
	// The set is per tag: the same names in a following tag are not duplicates.
	two := manyAttrs(50) + "/>" + manyAttrs(50) + "/>"
	if err := drainAll(NewDecoderBytes([]byte("<r>" + two + "</r>"))); err != nil {
		t.Errorf("per-tag set leaked across tags: %v", err)
	}
}

func manyAttrs(n int) string {
	var b strings.Builder
	b.WriteString("<a")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, " k%d=\"v\"", i)
	}
	return b.String()
}

// Prefix resolution must stay O(1) per lookup however many bindings are live.
func TestManyNamespaceBindingsLinear(t *testing.T) {
	const n, elems = 40_000, 100_000
	var b strings.Builder
	b.WriteString("<a")
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, " xmlns:p%d=\"u%d\"", i, i)
	}
	b.WriteString(">")
	for i := 0; i < elems; i++ {
		b.WriteString("<p0:x/>")
	}
	b.WriteString("</a>")
	in := b.String()
	within(t, 2*time.Second, func() {
		if err := drainAll(NewDecoderBytes([]byte(in), MaxAttributes(0))); err != nil {
			t.Fatal(err)
		}
	})
}

// Shadowing, scope exit and unbound prefixes behave the same with the prefix
// index (many live bindings) as with the backwards scan (few), and match the
// frozen encoding/xml decoder.
func TestNamespaceIndexScoping(t *testing.T) {
	var decl strings.Builder
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&decl, ` xmlns:p%d="u%d"`, i, i)
	}
	cases := []string{
		`<a` + decl.String() + `><p3:x/><b xmlns:p3="inner"><p3:y/><p39:z/></b><p3:w/></a>`,
		`<a` + decl.String() + `><b xmlns:p3="i1"><c xmlns:p3="i2"><p3:v/></c><p3:u/></b><p3:t/></a>`,
		`<a` + decl.String() + `><b xmlns:q="x"><q:k/></b><q:bad/></a>`,
		`<a` + decl.String() + ` xmlns="d"><b xmlns:p0="again"><p0:m/></b><p0:n/><o/></a>`,
		`<a xmlns:p="1"><b xmlns:p="2"><p:x/></b><p:y/></a>`,
	}
	for _, in := range cases {
		if d := diffExpected(t, in, strictCfg); d != "" {
			t.Errorf("%q\n%s", in, d)
		}
	}
}

// A document type declaration is returned as an opaque Directive: entities it
// declares are not expanded, so entity-expansion bombs and external entities
// (XXE) have nothing to act on.
func TestNoEntityExpansionFromDoctype(t *testing.T) {
	bomb := `<!DOCTYPE x [<!ENTITY a "&b;&b;"><!ENTITY b "&c;&c;"><!ENTITY c "lol">` +
		`<!ENTITY x SYSTEM "file:///etc/passwd">]><r>&a;&x;</r>`
	err := drainAll(NewDecoderBytes([]byte(bomb)))
	if err == nil || !strings.Contains(err.Error(), "invalid character entity") {
		t.Fatalf("declared entities must stay unknown: %v", err)
	}
}

// With MaxTokenSize(0) an unterminated construct is buffered until EOF (as in
// encoding/xml); with a limit (16 MiB by default), memory is bounded.
func TestMaxTokenSizeBoundsBuffering(t *testing.T) {
	for _, head := range []string{"<a><!--", "<a b=\"", "<a><![CDATA[", "<a><?pi "} {
		r := io.MultiReader(strings.NewReader(head), io.LimitReader(repeatReader{'x'}, 64<<20))
		err := drainAll(NewDecoder(r, MaxTokenSize(1<<20)))
		if err == nil || !strings.Contains(err.Error(), "MaxTokenSize") {
			t.Errorf("%q: want MaxTokenSize error, got %v", head, err)
		}
	}
	// A long text run is split with SplitText, not rejected or buffered.
	r := io.MultiReader(strings.NewReader("<a>"), io.LimitReader(repeatReader{'x'}, 64<<20), strings.NewReader("</a>"))
	if err := drainAll(NewDecoder(r, MaxTokenSize(1<<20), SplitText(true))); err != nil {
		t.Errorf("long text: %v", err)
	}
	// Whole text over the limit is an error that names SplitText.
	r = io.MultiReader(strings.NewReader("<a>"), io.LimitReader(repeatReader{'x'}, 64<<20), strings.NewReader("</a>"))
	if err := drainAll(NewDecoder(r, MaxTokenSize(1<<20))); err == nil || !strings.Contains(err.Error(), "SplitText") {
		t.Errorf("long text without SplitText: want error naming SplitText, got %v", err)
	}
}
