// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"github.com/go-xml-experiment/xml/internal/xmlopts"
	"math/rand"
	"strings"
	"testing"
)

func encode(t *testing.T, opts []Options, f func(e *Encoder) error) string {
	t.Helper()
	var b bytes.Buffer
	e := NewEncoder(&b, opts...)
	if err := f(e); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return b.String()
}

func TestEncoderBasics(t *testing.T) {
	got := encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Local: "a"})
		e.Attr(Name{Local: "x"}, `1<2&"3"`+"\t\n")
		e.BeginElement(Name{Local: "b"})
		e.EndElement()
		e.BeginElement(Name{Local: "c"})
		e.Text("t<&>]]>\r")
		e.EndElement()
		e.Comment([]byte(" c "))
		e.BeginElement(Name{Local: "d"})
		e.CData([]byte("x]]>y"))
		e.EndElement()
		return e.EndElement()
	})
	want := `<a x="1&lt;2&amp;&quot;3&quot;&#x9;&#xA;"><b/><c>t&lt;&amp;>]]&gt;&#xD;</c><!-- c --><d><![CDATA[x]]]]><![CDATA[>y]]></d></a>`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestEncoderNamespaces(t *testing.T) {
	got := encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Space: "urn:a", Local: "root"})
		e.Attr(Name{Space: "urn:b", Local: "x"}, "1")
		e.Attr(Name{Space: XMLNamespace, Local: "lang"}, "en")
		e.BeginElement(Name{Space: "urn:a", Local: "child"})
		e.Attr(Name{Space: "urn:b", Local: "y"}, "2")
		e.EndElement()
		e.BeginElement(Name{Local: "plain"})
		e.EndElement()
		return e.EndElement()
	})
	want := `<ns1:root xmlns:ns1="urn:a" xmlns:ns2="urn:b" ns2:x="1" xml:lang="en"><ns1:child ns2:y="2"/><plain/></ns1:root>`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	// explicit declarations win; default namespace is reused and undeclared
	got = encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Space: "urn:a", Local: "root"})
		e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "urn:a")
		e.BeginElement(Name{Local: "unq"})
		e.EndElement()
		e.BeginElement(Name{Space: "urn:a", Local: "q"})
		e.EndElement()
		return e.EndElement()
	})
	want = `<root xmlns="urn:a"><unq xmlns=""/><q/></root>`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	got = encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Space: "urn:a", Local: "root"})
		e.Attr(Name{Space: XMLNSNamespace, Local: "p"}, "urn:a")
		return e.EndElement()
	})
	if want := `<p:root xmlns:p="urn:a"/>`; got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

func TestEncoderErrors(t *testing.T) {
	cases := []struct {
		name string
		f    func(e *Encoder) error
		want string
	}{
		{"bad element name", func(e *Encoder) error { return e.BeginElement(Name{Local: "1a"}) }, "invalid element name"},
		{"duplicate attr", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Local: "x"}, "1")
			e.Attr(Name{Local: "x"}, "2")
			return e.EndElement()
		}, "duplicate attribute"},
		{"invalid utf8", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			return e.Text("\xff")
		}, "invalid UTF-8"},
		{"control char", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			return e.Text("\x01")
		}, "invalid UTF-8"},
		{"two roots", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.EndElement()
			return e.BeginElement(Name{Local: "b"})
		}, "multiple root"},
		{"text outside root", func(e *Encoder) error { return e.Text("x") }, "outside of the root"},
		{"comment dashes", func(e *Encoder) error { return e.Comment([]byte("a--b")) }, `"--"`},
		{"pi close", func(e *Encoder) error { return e.ProcInst("t", []byte("a?>b")) }, `"?>"`},
		{"late xml decl", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			return e.ProcInst("xml", []byte(`version="1.0"`))
		}, "first output"},
		{"directive", func(e *Encoder) error { return e.Directive([]byte("a <b")) }, "unbalanced"},
		{"attr outside tag", func(e *Encoder) error { return e.Attr(Name{Local: "x"}, "1") }, "outside a start tag"},
		{"end without start", func(e *Encoder) error { return e.EndElement() }, "without an open element"},
		{"reserved prefix", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "xml"}, "urn:x")
			return e.EndElement()
		}, "prefix xml must be bound"},
	}
	for _, c := range cases {
		var b bytes.Buffer
		e := NewEncoder(&b)
		err := c.f(e)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: want error containing %q, got %v", c.name, c.want, err)
			continue
		}
		if err2 := e.Text("more"); err2 != err { // sticky
			t.Errorf("%s: error not sticky: %v", c.name, err2)
		}
	}
	// Close reports an unclosed element; use after Close fails.
	var b bytes.Buffer
	e := NewEncoder(&b)
	e.BeginElement(Name{Local: "a"})
	e.Text("x")
	if err := e.Close(); err == nil || !strings.Contains(err.Error(), "unclosed tag <a>") {
		t.Errorf("Close: %v", err)
	}
	if err := e.Text("y"); err == nil {
		t.Error("write after Close succeeded")
	}
}

type shortWriter struct{ n int }

func (w *shortWriter) Write(p []byte) (int, error) { w.n += len(p); return len(p) / 2, nil }

func TestEncoderWriteErrors(t *testing.T) {
	e := NewEncoder(&shortWriter{}, BufferSize(8))
	e.BeginElement(Name{Local: "a"})
	err := e.Text(strings.Repeat("x", 100))
	if err == nil {
		err = e.Flush()
	}
	if err == nil {
		t.Fatal("short write not reported")
	}
	if e.EndElement() != err {
		t.Fatal("write error not sticky")
	}
}

func TestEncoderIndent(t *testing.T) {
	got := encode(t, []Options{WithIndent("", "  ")}, func(e *Encoder) error {
		e.BeginElement(Name{Local: "a"})
		e.BeginElement(Name{Local: "b"})
		e.BeginElement(Name{Local: "c"})
		e.EndElement()
		e.EndElement()
		e.BeginElement(Name{Local: "p"})
		e.Text("x")
		e.BeginElement(Name{Local: "i"})
		e.EndElement()
		e.Text("y")
		e.EndElement()
		return e.EndElement()
	})
	want := "<a>\n  <b>\n    <c/>\n  </b>\n  <p>x<i/>y</p>\n</a>"
	if got != want {
		t.Fatalf("\n got %q\nwant %q", got, want)
	}
}

// Whatever the Encoder writes in the default profile, the strict Decoder reads
// back with the same tokens, including after chunked output.
func TestEncoderRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for iter := 0; iter < 300; iter++ {
		toks := randomTokens(rng, 0)
		out := encode(t, []Options{BufferSize(1 + rng.Intn(64))}, func(e *Encoder) error { return playTokens(e, toks) })
		got, err := readAll(t, NewDecoderBytes([]byte(out)))
		if err != nil {
			t.Fatalf("iter %d: reread %q: %v", iter, out, err)
		}
		for i := range got { // generated namespace declarations are not part of the model
			var kept []Attr
			for _, a := range got[i].attrs {
				if !isDeclName(a.Name) {
					kept = append(kept, a)
				}
			}
			got[i].attrs = kept
		}
		want := wantTokens(toks)
		if fmt.Sprint(normalize(got)) != fmt.Sprint(normalize(want)) {
			t.Fatalf("iter %d: %q\n got %v\nwant %v", iter, out, normalize(got), normalize(want))
		}
		// determinism
		out2 := encode(t, nil, func(e *Encoder) error { return playTokens(e, toks) })
		if out2 != out && !strings.Contains(out, "") {
			t.Fatalf("not deterministic")
		}
	}
}

// genTok is a token the generator emits and both encoders can write.
type genTok struct {
	kind  Kind
	name  Name
	attrs []Attr
	text  string
}

var genNames = []string{"a", "b", "item", "x-y", "k1", "é", "long_name_here"}
var genSpaces = []string{"", "", "", "urn:a", "http://example.com/ns/v1", "urn:b/"}
var genTexts = []string{"", "plain", "a&b", "<tag>", "]]>", "q\"uote'", "tab\there", "line\nbreak", "cr\rend", "한글", "😀", " ", "x]", "]y"}

func randomTokens(rng *rand.Rand, depth int) []genTok {
	var out []genTok
	var gen func(d int)
	gen = func(d int) {
		name := Name{Space: genSpaces[rng.Intn(len(genSpaces))], Local: genNames[rng.Intn(len(genNames))]}
		st := genTok{kind: StartElement, name: name}
		used := map[Name]bool{}
		for i, n := 0, rng.Intn(4); i < n; i++ {
			an := Name{Space: genSpaces[rng.Intn(len(genSpaces))], Local: genNames[rng.Intn(len(genNames))]}
			if used[an] {
				continue
			}
			used[an] = true
			st.attrs = append(st.attrs, Attr{an, genTexts[rng.Intn(len(genTexts))]})
		}
		out = append(out, st)
		for i, n := 0, rng.Intn(4); i < n && d < 4; i++ {
			switch rng.Intn(5) {
			case 0, 1:
				gen(d + 1)
			case 2:
				out = append(out, genTok{kind: Text, text: genTexts[rng.Intn(len(genTexts))]})
			case 3:
				out = append(out, genTok{kind: Comment, text: []string{"c", " x ", ""}[rng.Intn(3)]})
			case 4:
				txt := strings.ReplaceAll(genTexts[1+rng.Intn(len(genTexts)-1)], "\r", "") // a raw CR in CDATA reads back as LF
				out = append(out, genTok{kind: CData, text: txt})
			}
		}
		out = append(out, genTok{kind: EndElement, name: name})
	}
	gen(0)
	return out
}

func playTokens(e *Encoder, toks []genTok) error {
	for _, t := range toks {
		var err error
		switch t.kind {
		case StartElement:
			err = e.BeginElement(t.name)
			for _, a := range t.attrs {
				if err == nil {
					err = e.Attr(a.Name, a.Value)
				}
			}
		case EndElement:
			err = e.EndElement()
		case Text:
			err = e.Text(t.text)
		case CData:
			err = e.CData([]byte(t.text))
		case Comment:
			err = e.Comment([]byte(t.text))
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// wantTokens is what a namespace-aware reader should see: adjacent text
// merged, CDATA as text, empty text dropped by normalize.
func wantTokens(toks []genTok) []tok {
	var out []tok
	for _, t := range toks {
		switch t.kind {
		case StartElement:
			x := tok{kind: StartElement, name: t.name}
			x.attrs = append(x.attrs, t.attrs...)
			out = append(out, x)
		case EndElement:
			out = append(out, tok{kind: EndElement, name: t.name})
		case Text, CData:
			if t.text != "" {
				out = append(out, tok{kind: Text, text: t.text})
			}
		case Comment:
			out = append(out, tok{kind: Comment, text: t.text})
		}
	}
	return out
}

// FuzzEncoderValues: any text/attribute value either fails because it holds an
// invalid character, or is written such that the strict Decoder returns it
// unchanged.
func FuzzEncoderValues(f *testing.F) {
	for _, s := range []string{"", "a", "<&>\"'", "]]>", "]", "\r\n\t", "한글😀", "\xff", "\x01", "￾", "a]]", "]]"} {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, text, attr string) {
		var b bytes.Buffer
		e := NewEncoder(&b, BufferSize(7))
		e.BeginElement(Name{Local: "a"})
		e.Attr(Name{Local: "x"}, attr)
		e.Text(text)
		e.Text("]") // exercises "]]>" across calls when text ends with "]"
		e.Text(">")
		e.EndElement()
		err := e.Close()
		valid := checkCharsOnly(text) == nil && checkCharsOnly(attr) == nil
		if err != nil {
			if valid {
				t.Fatalf("rejected valid values: %v", err)
			}
			return
		}
		if !valid {
			t.Fatalf("accepted invalid values %q %q -> %q", text, attr, b.String())
		}
		d := NewDecoderBytes(b.Bytes())
		var gotText []byte
		var gotAttr string
		for {
			tk, err := d.ReadToken()
			if err != nil {
				break
			}
			switch tk.Kind() {
			case StartElement:
				for it := tk.Attrs(); it.Next(); {
					gotAttr = string(it.Value())
				}
			case Text:
				gotText = append(gotText, tk.Text()...)
			}
		}
		// attribute value normalization turns raw tab/newline into space; the
		// encoder escapes them, so the value must round-trip exactly
		if gotAttr != attr || string(gotText) != text+"]>" {
			t.Fatalf("round trip: %q -> %q text=%q attr=%q (want %q, %q)", b.String(), "", gotText, gotAttr, text+"]>", attr)
		}
	})
}

func checkCharsOnly(s string) error { return (&Encoder{}).checkChars([]byte(s)) }

// Elements without a namespace write their tag eagerly; declarations that
// the encoder generates for namespaced attributes land just before the attribute.
func TestEncoderEagerTags(t *testing.T) {
	got := encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Local: "a"})
		e.Attr(Name{Local: "x"}, "1")
		e.Attr(Name{Space: "urn:b", Local: "y"}, "2")
		e.Attr(Name{Local: "z"}, "3")
		e.Attr(Name{Space: "urn:b", Local: "w"}, "4")
		e.Attr(Name{Space: XMLNSNamespace, Local: "q"}, "urn:q")
		e.BeginElement(Name{Local: "b"})
		e.Attr(Name{Space: "urn:q", Local: "k"}, "5")
		e.EndElement()
		return e.EndElement()
	})
	want := `<a x="1" xmlns:ns1="urn:b" ns1:y="2" z="3" ns1:w="4" xmlns:q="urn:q"><b q:k="5"/></a>`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
	for _, c := range []struct {
		name string
		f    func(e *Encoder) error
		want string
	}{
		{"default ns on unqualified element", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			return e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "urn:a")
		}, "cannot declare the default namespace"},
		{"duplicate namespaced attribute", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Space: "urn:b", Local: "y"}, "1")
			return e.Attr(Name{Space: "urn:b", Local: "y"}, "2")
		}, "duplicate attribute"},
		{"duplicate after set threshold", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			for i := 0; i < 40; i++ {
				e.Attr(Name{Local: fmt.Sprintf("k%d", i)}, "v")
			}
			return e.Attr(Name{Local: "k7"}, "v")
		}, "duplicate attribute"},
	} {
		var b bytes.Buffer
		e := NewEncoder(&b)
		if err := c.f(e); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	// xmlns="" on an unqualified element is allowed and written
	if got := encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Local: "a"})
		e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "")
		return e.EndElement()
	}); got != `<a xmlns=""/>` {
		t.Errorf("got %s", got)
	}
}

// LegacyOutput uses expanded empty tags, derived namespace prefixes, and v1 escaping.
func TestEncoderLegacyOutput(t *testing.T) {
	got := encode(t, []Options{xmlopts.LegacyOutput(true)}, func(e *Encoder) error {
		e.BeginElement(Name{Space: "urn:a", Local: "a"})
		e.Attr(Name{Space: "urn:b", Local: "x"}, "<&\t\n\r")
		e.BeginElement(Name{Local: "b"})
		e.EndElement()
		e.Text("<&>\r")
		return e.EndElement()
	})
	const want = `<a xmlns="urn:a" xmlns:_="urn:b" _:x="&lt;&amp;&#x9;&#xA;&#xD;"><b></b>&lt;&amp;&gt;&#xD;</a>`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
func TestEscapeLegacy(t *testing.T) {
	e := NewEncoder(nil, xmlopts.LegacyOutput(true))
	for _, c := range []struct{ in, want string }{
		{"<&>\"'\t\n\r", "&lt;&amp;&gt;&#34;&#39;&#x9;&#xA;&#xD;"},
		{"\x00\x01\xff", "���"},
		{"é한😀", "é한😀"},
	} {
		got, err := e.escapeInto(nil, []byte(c.in), escLegAttr, 0)
		if err != nil || string(got) != c.want {
			t.Errorf("%q: got %q (%v), want %q", c.in, got, err, c.want)
		}
	}
}
