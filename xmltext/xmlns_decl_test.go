// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
	"strings"
	"testing"
)

// The Decoder names a declaration by the xmlns namespace; any other attribute,
// whatever its URI, never gets that Space.
func TestDecoderDeclarationNames(t *testing.T) {
	for _, tc := range []struct {
		in    string
		perm  bool
		attrs string
	}{
		{`<a xmlns:p="u" xmlns="d" p:x="1" y="2"/>`, false,
			`[{{"` + XMLNSNamespace + `" "p"} "u"} {{"` + XMLNSNamespace + `" "xmlns"} "d"} {{"u" "x"} "1"} {{"" "y"} "2"}]`},
		{`<a xmlns:q="xmlns" q:foo="bar"/>`, false,
			`[{{"` + XMLNSNamespace + `" "q"} "xmlns"} {{"xmlns" "foo"} "bar"}]`},
		{`<a xmlns:xml="` + XMLNamespace + `" xml:lang="en"/>`, false,
			`[{{"` + XMLNSNamespace + `" "xml"} "` + XMLNamespace + `"} {{"` + XMLNamespace + `" "lang"} "en"}]`},
		// Permissive: an unbound prefix stands for itself, and so does a prefix
		// bound to the xmlns namespace; nothing but a declaration has that Space.
		{`<a u:x="1"/>`, true, `[{{"u" "x"} "1"}]`},
		{`<a xmlns:p="` + XMLNSNamespace + `" p:x="1"/>`, true,
			`[{{"` + XMLNSNamespace + `" "p"} "` + XMLNSNamespace + `"} {{"p" "x"} "1"}]`},
		{`<a xmlns:xmlns="u" xmlns="d"/>`, true,
			`[{{"xmlns" "xmlns"} "u"} {{"` + XMLNSNamespace + `" "xmlns"} "d"}]`},
		{`<xmlns:r xmlns:p="u"/>`, true, `[{{"` + XMLNSNamespace + `" "p"} "u"}]`},
	} {
		var opts []Options
		if tc.perm {
			opts = append(opts, Permissive(true))
		}
		d := NewDecoderBytes([]byte(tc.in), opts...)
		tk, err := d.ReadToken()
		if err != nil {
			t.Errorf("%s: %v", tc.in, err)
			continue
		}
		var got []Attr
		for it := tk.Attrs(); it.Next(); {
			got = append(got, Attr{it.Name(), string(it.Value())})
		}
		if fmt.Sprintf("%q", got) != tc.attrs {
			t.Errorf("%s\n got %q\nwant %s", tc.in, got, tc.attrs)
		}
		// RawName keeps the lexical prefix, and Clone keeps the resolved names.
		var raws, cloned []Name
		for it := tk.Attrs(); it.Next(); {
			raws = append(raws, it.RawName())
		}
		for it := tk.Clone().Attrs(); it.Next(); {
			cloned = append(cloned, it.Name())
		}
		for i, a := range got {
			if cloned[i] != a.Name {
				t.Errorf("%s: Clone attribute %d is %v, want %v", tc.in, i, cloned[i], a.Name)
			}
			if isDeclName(a.Name) && raws[i].Space != "xmlns" && raws[i] != (Name{"", "xmlns"}) {
				t.Errorf("%s: RawName of declaration %d is %v", tc.in, i, raws[i])
			}
		}
	}
}

// In the default profile a declaration is exactly Space == the xmlns
// namespace; Space "xmlns" is an ordinary URI and gets a generated prefix.
func TestEncoderDeclarationNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		f    func(e *Encoder) error
		want string
	}{
		{"prefix declaration", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:a", Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "p"}, "urn:a")
			e.Attr(Name{Space: "urn:a", Local: "x"}, "1")
			return e.EndElement()
		}, `<p:a xmlns:p="urn:a" p:x="1"/>`},
		{"default declaration", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:d", Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "urn:d")
			return e.EndElement()
		}, `<a xmlns="urn:d"/>`},
		{"xmlns is an ordinary URI on attributes", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Space: "xmlns", Local: "foo"}, "bar")
			return e.EndElement()
		}, `<a xmlns:ns1="xmlns" ns1:foo="bar"/>`},
		{"xmlns is an ordinary URI on elements", func(e *Encoder) error {
			e.BeginElement(Name{Space: "xmlns", Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "q"}, "xmlns")
			e.Attr(Name{Space: "xmlns", Local: "p"}, "v")
			return e.EndElement()
		}, `<q:a xmlns:q="xmlns" q:p="v"/>`},
		{"xmlns URI beside a declaration of the same local name", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "ns1"}, "urn:x")
			e.Attr(Name{Space: "xmlns", Local: "ns1"}, "1")
			return e.EndElement()
		}, `<a xmlns:ns1="urn:x" xmlns:ns2="xmlns" ns2:ns1="1"/>`},
		{"xml prefix", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "xml"}, XMLNamespace)
			e.Attr(Name{Space: XMLNamespace, Local: "lang"}, "en")
			return e.EndElement()
		}, `<a xmlns:xml="` + XMLNamespace + `" xml:lang="en"/>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := encode(t, nil, tc.f); got != tc.want {
				t.Errorf("\n got %s\nwant %s", got, tc.want)
			}
		})
	}
	for _, tc := range []struct {
		name string
		f    func(e *Encoder) error
		want string
	}{
		{"bare xmlns attribute, eager", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			return e.Attr(Name{Local: "xmlns"}, "urn:x")
		}, `must be a namespace declaration: use Name{Space: XMLNSNamespace, Local: "xmlns"}`},
		{"bare xmlns attribute, namespaced element", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:p", Local: "a"})
			return e.Attr(Name{Local: "xmlns"}, "urn:p")
		}, `must be a namespace declaration: use Name{Space: XMLNSNamespace, Local: "xmlns"}`},
		{"bare empty xmlns attribute", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			return e.Attr(Name{Local: "xmlns"}, "")
		}, `must be a namespace declaration: use Name{Space: XMLNSNamespace, Local: "xmlns"}`},
		{"element in the xmlns namespace", func(e *Encoder) error {
			e.BeginElement(Name{Space: XMLNSNamespace, Local: "a"})
			return e.EndElement()
		}, "reserved for declarations"},
		{"xmlns prefix", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:a", Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, XMLNSNamespace)
			return e.EndElement()
		}, "cannot be bound"},
		{"xmlns namespace on a prefix", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:a", Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "p"}, XMLNSNamespace)
			return e.EndElement()
		}, "cannot be bound"},
		{"xmlns namespace on a prefix, eager", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			return e.Attr(Name{Space: XMLNSNamespace, Local: "p"}, XMLNSNamespace)
		}, "cannot be bound"},
		{"xml namespace as default", func(e *Encoder) error {
			e.BeginElement(Name{Space: XMLNamespace, Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, XMLNamespace)
			return e.EndElement()
		}, "can only be bound to prefix xml"},
		{"xml namespace on another prefix", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:a", Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "p"}, XMLNamespace)
			return e.EndElement()
		}, "can only be bound to prefix xml"},
		{"xml prefix elsewhere", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:a", Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "xml"}, "urn:a")
			return e.EndElement()
		}, "prefix xml must be bound"},
		{"generated prefix then the same declaration", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Space: "xmlns", Local: "ns1"}, "1")
			return e.Attr(Name{Space: XMLNSNamespace, Local: "ns1"}, "urn:x")
		}, "duplicate attribute"},
		{"empty prefixed declaration", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:a", Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "p"}, "")
			return e.EndElement()
		}, "empty namespace"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			err := tc.f(NewEncoder(&b))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want substring %q (output %q)", err, tc.want, b.String())
			}
		})
	}
}

// LegacyOutput keeps encoding/xml's reading of "xmlns" names: Space "xmlns" is
// a namespace URI that gets a derived prefix, and a bare xmlns attribute is
// written as is. The bytes are a fixed expectation from the original stdlib Encoder.
func TestEncoderLegacyXMLNSNames(t *testing.T) {
	attrs := []Attr{
		{Name{"xmlns", "x"}, "space"},
		{Name{"", "xmlns"}, "d"},
		{Name{XMLNSNamespace, "p"}, "v"},
		{Name{"xmlns", "xmlns"}, "w"},
	}
	got := encode(t, []Options{xmlopts.LegacyOutput(true)}, func(e *Encoder) error {
		e.BeginElement(Name{Local: "a"})
		for _, a := range attrs {
			e.Attr(a.Name, a.Value)
		}
		return e.EndElement()
	})
	const want = `<a xmlns:_xmlns="xmlns" _xmlns:x="space" xmlns="d" xmlns:_xmlns_1="http://www.w3.org/2000/xmlns/" _xmlns_1:p="v" _xmlns:xmlns="w"></a>`
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// WriteToken writes declarations before the other attributes of a tag, so a
// prefix declared after its first use is not replaced by a generated one.
func TestWriteTokenDeclarationsFirst(t *testing.T) {
	const in = `<a p:x="1" xmlns:p="u" y="2" xmlns="d"><p:b/></a>`
	var b bytes.Buffer
	e := NewEncoder(&b)
	d := NewDecoderBytes([]byte(in))
	for {
		tk, err := d.ReadToken()
		if err != nil {
			break
		}
		if err := e.WriteToken(tk); err != nil {
			t.Fatal(err)
		}
		if tk.Kind() == StartElement && tk.Name().Local == "a" { // owned tokens take the same path
			b2 := new(bytes.Buffer)
			e2 := NewEncoder(b2)
			if err := e2.WriteToken(tk.Clone()); err != nil || e2.EndElement() != nil || e2.Close() != nil {
				t.Fatal(err)
			}
			if got := b2.String(); !strings.HasPrefix(got, `<a xmlns:p="u" xmlns="d" p:x="1" y="2"`) {
				t.Errorf("clone: %s", got)
			}
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if want := `<a xmlns:p="u" xmlns="d" p:x="1" y="2"><p:b/></a>`; b.String() != want {
		t.Errorf("\n got %s\nwant %s", b.String(), want)
	}
}
