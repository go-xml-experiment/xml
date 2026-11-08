// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"testing"

	"github.com/Clickin/go-experiment-xml/xmltext"
)

// A []Attr any,attr field keeps namespace declarations, so that Marshal
// writes the prefixes it read and a prefix used only in an attribute value
// stays declared.
func TestAnyAttrKeepsDeclarations(t *testing.T) {
	type qnameDoc struct {
		XMLName Name           `xml:"urn:x root"`
		Type    string         `xml:"type,attr"`
		Attrs   []xmltext.Attr `xml:",any,attr"`
		Child   string         `xml:"urn:x child,omitempty"`
	}
	type oneAttr struct { // a single Attr does not take declarations
		XMLName Name         `xml:"urn:x root"`
		Any     xmltext.Attr `xml:",any,attr"`
	}
	tests := []struct {
		name string
		in   string
		v    func() any
		want string
	}{{
		name: "QNameInContent",
		in:   `<root xmlns="urn:x" xmlns:p="urn:p" type="p:Foo"/>`,
		v:    func() any { return new(qnameDoc) },
		want: `<root xmlns="urn:x" xmlns:p="urn:p" type="p:Foo"/>`,
	}, {
		name: "Prefixed",
		in:   `<p:root xmlns:p="urn:x" type="t"><p:child>c</p:child></p:root>`,
		v:    func() any { return new(qnameDoc) },
		want: `<p:root xmlns:p="urn:x" type="t"><p:child>c</p:child></p:root>`,
	}, {
		name: "SingleAttr",
		in:   `<root xmlns="urn:x" a="1"/>`,
		v:    func() any { return new(oneAttr) },
		want: `<ns1:root xmlns:ns1="urn:x" a="1"/>`,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := []byte(tt.in)
			for pass := 1; pass <= 2; pass++ {
				v := tt.v()
				if err := Unmarshal(b, v); err != nil {
					t.Fatalf("pass %d: Unmarshal(%s): %v", pass, b, err)
				}
				out, err := Marshal(v)
				if err != nil {
					t.Fatalf("pass %d: Marshal: %v", pass, err)
				}
				if string(out) != tt.want {
					t.Fatalf("pass %d:\n got %s\nwant %s", pass, out, tt.want)
				}
				b = out
			}
		})
	}
}

// A default namespace declaration in a []Attr any,attr field of an element
// in no namespace is dropped on Marshal: such an element cannot declare one.
// Unmarshal does not produce this value, since <root xmlns="u"> is not in
// no namespace; it can only be built by hand.
func TestAnyAttrDefaultDeclOnNoNamespaceElement(t *testing.T) {
	type plainDoc struct {
		XMLName struct{}       `xml:"root"`
		Attrs   []xmltext.Attr `xml:",any,attr"`
	}
	v := plainDoc{Attrs: []xmltext.Attr{
		{Name: Name{Space: xmltext.XMLNSNamespace, Local: "xmlns"}, Value: "urn:x"},
		{Name: Name{Space: xmltext.XMLNSNamespace, Local: "p"}, Value: "urn:p"},
		{Name: Name{Local: "a"}, Value: "1"},
	}}
	out, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<root xmlns:p="urn:p" a="1"/>`; string(out) != want {
		t.Fatalf("got %s, want %s", out, want)
	}
	if err := Unmarshal([]byte(`<root xmlns="urn:x"/>`), new(plainDoc)); err == nil {
		t.Fatal("Unmarshal of <root xmlns=\"urn:x\"> into a root in no namespace succeeded")
	}
}
