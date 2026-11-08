// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml_test

import (
	"bytes"
	"github.com/Clickin/go-experiment-xml/v1"
	"github.com/Clickin/go-experiment-xml/xmltext"
	"io"
	"testing"
)

// These tests document semantic differences between the v1 API running on v2
// and xmltext's default profile. Neither side requires a legacy implementation.
func TestV1V2AcceptanceDifferences(t *testing.T) {
	// The v1 API accepts fragments, unbound prefixes and duplicate attributes.
	// xmltext defaults to one well-formed document with namespace validation.
	for _, in := range []string{`<a/><b/>`, `<p:a/>`, `<a x="1" x="2"/>`} {
		t.Run(in, func(t *testing.T) {
			v1 := xml.NewDecoder(bytes.NewBufferString(in))
			for {
				_, err := v1.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("v1: %v", err)
				}
			}
			v2 := xmltext.NewDecoderBytes([]byte(in))
			for {
				_, err := v2.ReadToken()
				if err != nil {
					if err == io.EOF {
						t.Fatal("xmltext accepted input")
					}
					break
				}
			}
		})
	}
}

func TestV1V2AttributeWhitespace(t *testing.T) {
	// v1 preserves literal attribute whitespace after line-end normalization.
	// xmltext applies XML 1.0 attribute-value normalization to spaces.
	const in = "<a x=\"a\tb\nc\r\nd&#9;\"/>"
	v1 := xml.NewDecoder(bytes.NewBufferString(in))
	tk, err := v1.Token()
	if err != nil {
		t.Fatal(err)
	}
	if got := tk.(xml.StartElement).Attr[0].Value; got != "a\tb\nc\nd\t" {
		t.Fatalf("v1 value %q", got)
	}
	v2 := xmltext.NewDecoderBytes([]byte(in))
	token, err := v2.ReadToken()
	if err != nil {
		t.Fatal(err)
	}
	attrs := token.Attrs()
	if !attrs.Next() || string(attrs.Value()) != "a b c d\t" {
		t.Fatalf("xmltext value %q", attrs.Value())
	}
}

func TestV1V2CDATA(t *testing.T) {
	// v1 exposes CDATA as CharData; xmltext retains its lexical token kind.
	const in = `<a><![CDATA[text]]></a>`
	v1 := xml.NewDecoder(bytes.NewBufferString(in))
	v1.Token()
	tk, err := v1.Token()
	if err != nil {
		t.Fatal(err)
	}
	if text, ok := tk.(xml.CharData); !ok || string(text) != "text" {
		t.Fatalf("v1 token %#v", tk)
	}
	v2 := xmltext.NewDecoderBytes([]byte(in))
	v2.ReadToken()
	token, err := v2.ReadToken()
	if err != nil || token.Kind() != xmltext.CData || string(token.Text()) != "text" {
		t.Fatalf("xmltext token %v (%v)", token, err)
	}
}

func TestV1V2EmptyElementOutput(t *testing.T) {
	// v1 emits separate start/end tags; xmltext emits an empty-element tag.
	var v1out, v2out bytes.Buffer
	v1 := xml.NewEncoder(&v1out)
	start := xml.StartElement{Name: xml.Name{Local: "a"}}
	if err := v1.EncodeToken(start); err != nil {
		t.Fatal(err)
	}
	if err := v1.EncodeToken(start.End()); err != nil {
		t.Fatal(err)
	}
	if err := v1.Close(); err != nil {
		t.Fatal(err)
	}
	v2 := xmltext.NewEncoder(&v2out)
	if err := v2.BeginElement(xmltext.Name{Local: "a"}); err != nil {
		t.Fatal(err)
	}
	if err := v2.EndElement(); err != nil {
		t.Fatal(err)
	}
	if err := v2.Close(); err != nil {
		t.Fatal(err)
	}
	if v1out.String() != `<a></a>` || v2out.String() != `<a/>` {
		t.Fatalf("v1 %q, xmltext %q", v1out.String(), v2out.String())
	}
}
