// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// rtEvent is what a round trip must preserve about one token.
type rtEvent struct {
	kind  Kind
	name  Name
	attrs map[Name]string // resolved name -> value, declarations included
	text  string
}

func (e rtEvent) String() string {
	return fmt.Sprintf("%v %q attrs=%q text=%q", e.kind, e.name, e.attrs, e.text)
}

// readEvents reads every token of in (default profile). Adjacent Text tokens
// (and adjacent CData tokens) are coalesced: how a run is split is not part of
// the token stream. With window > 0 the Decoder uses SplitText with that piece
// size, and toks holds the pieces.
func readEvents(in []byte, window int) (evs []rtEvent, toks []Token, err error) {
	d := NewDecoderBytes(in, SplitText(window > 0))
	if window > 0 {
		d.eng.textLimit = window
	}
	for {
		tk, err := d.ReadToken()
		if err == io.EOF {
			return evs, toks, nil
		}
		if err != nil {
			return nil, nil, err
		}
		ev := rtEvent{kind: tk.Kind()}
		switch tk.Kind() {
		case StartElement:
			ev.name = tk.Name()
			ev.attrs = map[Name]string{}
			for it := tk.Attrs(); it.Next(); {
				ev.attrs[it.Name()] = string(it.Value())
			}
		case EndElement:
			ev.name = tk.Name()
		case ProcInst:
			ev.name = tk.Name()
			ev.text = string(tk.Text())
		default:
			ev.text = string(tk.Text())
		}
		toks = append(toks, tk.Clone())
		if (ev.kind == Text || ev.kind == CData) && ev.text == "" {
			continue // an empty CDATA section has no content; the Encoder writes nothing for it
		}
		if n := len(evs); n > 0 && (ev.kind == Text || ev.kind == CData) && evs[n-1].kind == ev.kind {
			evs[n-1].text += ev.text
			continue
		}
		evs = append(evs, ev)
	}
}

func sameEvents(a, b []rtEvent) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x.kind != y.kind || x.name != y.name || x.text != y.text || len(x.attrs) != len(y.attrs) {
			return false
		}
		for k, v := range x.attrs {
			if w, ok := y.attrs[k]; !ok || w != v {
				return false
			}
		}
	}
	return true
}

// cdataSections counts the non-empty CDATA sections in a token sequence: a
// section ends at a CData token that is not Partial.
func cdataSections(toks []Token) (n int) {
	empty := true
	for _, tk := range toks {
		if tk.Kind() != CData {
			continue
		}
		empty = empty && len(tk.Text()) == 0
		if !tk.Partial() {
			if !empty {
				n++
			}
			empty = true
		}
	}
	return n
}

// checkRoundTrip: for every input the default Decoder accepts, writing its
// tokens with WriteToken must succeed, and the output must parse to the same
// tokens (kinds, resolved names, attribute sets with declarations, text).
// With window > 0 the tokens are read in pieces of that size (SplitText), the
// input must still be accepted, and the output must have the same number of
// CDATA sections.
func checkRoundTrip(t testing.TB, in []byte, window int) {
	t.Helper()
	want, _, err := readEvents(in, 0)
	if err != nil {
		return
	}
	if window > 0 {
		var splitEvs []rtEvent
		splitEvs, _, err = readEvents(in, window)
		if err != nil {
			t.Fatalf("input %q: whole mode accepts it, window %d fails: %v", in, window, err)
		}
		if !sameEvents(splitEvs, want) {
			t.Fatalf("input %q: window %d reads %v, whole mode %v", in, window, splitEvs, want)
		}
	}
	_, toks, _ := readEvents(in, window)
	var out bytes.Buffer
	e := NewEncoder(&out, BufferSize(7))
	for i, tk := range toks {
		if err := e.WriteToken(tk); err != nil {
			t.Fatalf("input %q: WriteToken of token %d (%v) failed: %v\noutput so far %q", in, i, want, err, out.String())
		}
	}
	if err := e.Close(); err != nil {
		t.Fatalf("input %q: Close: %v\noutput %q", in, err, out.String())
	}
	got, outToks, err := readEvents(out.Bytes(), 0)
	if err != nil {
		t.Fatalf("input %q: output %q does not parse: %v", in, out.String(), err)
	}
	if window > 0 {
		if a, b := cdataSections(toks), cdataSections(outToks); a != b {
			t.Fatalf("input %q window %d: %d CDATA sections written as %d: %q", in, window, a, b, out.String())
		}
	}
	if !sameEvents(got, want) {
		var b strings.Builder
		for i := 0; i < max(len(got), len(want)); i++ {
			var g, w string
			if i < len(got) {
				g = got[i].String()
			}
			if i < len(want) {
				w = want[i].String()
			}
			if g != w {
				fmt.Fprintf(&b, "\n token %d\n   got %s\n  want %s", i, g, w)
				break
			}
		}
		t.Fatalf("input %q: output %q differs:%s", in, out.String(), b.String())
	}
}

var roundTripSeeds = []string{
	"\ufeff<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"no\"?><?Xml-stylesheet?><r a=\"x\ny\tz\r\nw&#9;&#10;&#13;\"/>",
	`<?xml version="1.0"?><?xml-stylesheet href="a"?><r/>`,
	`<a>x<![CDATA[]]>y<![CDATA[]]></a>`,
	`<A xmlns:0="0000000000000000"/>`,
	`<A :=""/>`,
	`<a xmlns:q="xmlns" q:foo="bar"/>`,
	`<a xmlns:q="` + XMLNSNamespace + `"/>`,
	`<a xmlns:xmlns="x"/>`,
	`<a xmlns="xmlns" xmlns:xmlns2="xmlns" xmlns2:xmlns="1"/>`,
	`<a q:p="1" xmlns:q="u"/>`,
	`<r xmlns="urn:d" xmlns:p="urn:p" p:a="1" b="2"><c xmlns=""/><p:d xmlns:p="urn:q" p:a="3"/><e xmlns="urn:d"/></r>`,
	`<r xmlns:p="u" xmlns:q="u" p:x="1" q:y="2"><p:a q:x="3"/></r>`,
	`<a xmlns:xml="` + XMLNamespace + `" xml:lang="en"><b xml:space="preserve"/></a>`,
	`<p:a xmlns:p="u"><p:b xmlns:p="v"><p:c/></p:b><p:d/></p:a>`,
	`<a xmlns="u"><b xmlns:p="u" p:c="1"/></a>`,
	`<a xmlns:p="u"><b xmlns:q="u"><c xmlns="u"/></b></a>`,
	`<a x="1" xmlns:ns1="u" ns1:y="2" xmlns:ns2="v" ns2:y="3"/>`,
	`<ns1:a xmlns:ns1="u"><b xmlns:ns2="v" ns2:z="1" xmlns="w"/></ns1:a>`,
	`<?xml version="1.0"?><!DOCTYPE a [<!ELEMENT a ANY>]><a/>`,
	`<!-- c --><a><!-- d --><?pi x?><![CDATA[x<y]]>t&amp;&#65;</a><!-- e -->`,
	`<a>&lt;&gt;&apos;&quot;</a>`,
	"<a>\r\n\t xé\U0001F600</a>",
	`<a b="1&amp;2&#10;3"/>`,
	`<!DOCTYPE a <!--x-->><a/>`,
	`<!DOCTYPE a "<!-->"><a/>`,
	`<!DOCTYPE a [<!-- > -->]><a/>`,
	`<a><b><c><d/></c></b></a>`,
	` <a/> `,
}

func TestRoundTrip(t *testing.T) {
	for _, in := range roundTripSeeds {
		checkRoundTrip(t, []byte(in), 0)
		checkRoundTrip(t, []byte(in), 16)
	}
}

// The output of a namespace-heavy round trip, byte for byte.
func TestRoundTripOutput(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{`<a xmlns:q="xmlns" q:foo="bar"/>`, `<a xmlns:q="xmlns" q:foo="bar"/>`},
		{`<a q:p="1" xmlns:q="u"/>`, `<a xmlns:q="u" q:p="1"/>`},
		{`<r xmlns="d"><c xmlns=""><e/></c></r>`, `<r xmlns="d"><c xmlns=""><e/></c></r>`},
	} {
		d := NewDecoderBytes([]byte(tc.in))
		var out bytes.Buffer
		e := NewEncoder(&out)
		for {
			tk, err := d.ReadToken()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := e.WriteToken(tk); err != nil {
				t.Fatalf("%s: %v", tc.in, err)
			}
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		if out.String() != tc.want {
			t.Errorf("%s\n got %s\nwant %s", tc.in, out.String(), tc.want)
		}
	}
}

// A directive comment ends at a "-->" starting at least four bytes after its
// "<!--", as in the Decoder: "<!-->" never closes itself. (Review #10.)
func TestEncoderDirectiveComments(t *testing.T) {
	for _, tc := range []struct {
		dir string
		ok  bool
	}{
		{"DOCTYPE a <!-->", false},
		{"DOCTYPE a <!--->", false},
		{"DOCTYPE a <!---->", true},
		{"DOCTYPE a <!-- x -->", true},
		{"DOCTYPE a <!--> -->", true},
		{"DOCTYPE a <!--->--> ", true},
		{"DOCTYPE a <!-- > -->", true},
		{"DOCTYPE a <!--", false},
		{"DOCTYPE a [<!-- --> <!ELEMENT a ANY>]", true},
		{"DOCTYPE a [<!-->", false},
		{`DOCTYPE a "<!-->"`, true},
	} {
		var b bytes.Buffer
		e := NewEncoder(&b)
		err := e.Directive([]byte(tc.dir))
		if (err == nil) != tc.ok {
			t.Errorf("Directive(%q): err = %v, want ok=%v", tc.dir, err, tc.ok)
		}
		if err != nil {
			continue
		}
		// What the Encoder accepts the Decoder reads back as one directive.
		e.BeginElement(Name{Local: "r"})
		e.EndElement()
		if err := e.Close(); err != nil {
			t.Fatalf("Directive(%q): %v", tc.dir, err)
		}
		evs, _, derr := readEvents(b.Bytes(), 0)
		if derr != nil || len(evs) < 1 || evs[0].kind != Directive || evs[0].text != tc.dir {
			t.Errorf("Directive(%q): output %q reads back as %v (%v)", tc.dir, b.String(), evs, derr)
		}
	}
}

// FuzzRoundTrip runs whole mode and, with a piece size of 16 to 47 bytes taken
// from w, split mode.
func FuzzRoundTrip(f *testing.F) {
	for _, s := range roundTripSeeds {
		f.Add([]byte(s), uint8(0))
	}
	f.Add([]byte("<a><![CDATA[aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa]]><![CDATA[bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb]]>x&amp;y</a>"), uint8(3))
	f.Fuzz(func(t *testing.T, in []byte, w uint8) {
		checkRoundTrip(t, in, 0)
		checkRoundTrip(t, in, 16+int(w)%32)
	})
}
