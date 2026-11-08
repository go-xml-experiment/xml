// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml_test

import (
	"github.com/go-xml-experiment/xml/v1"
	"strings"
	"testing"
)

// hookType implements the user-visible Marshaler and Unmarshaler hooks with
// the exported encoding/xml.Decoder and encoding/xml.Encoder types. It
// checks that those types are the ones the v1-on-v2 implementation passes to
// callers: the hook methods only satisfy xml.Marshaler/xml.Unmarshaler if the
// parameter types are identical to the package's own.
type hookType struct {
	Words []string
}

var (
	_ xml.Marshaler   = hookType{}
	_ xml.Unmarshaler = (*hookType)(nil)
)

func (h hookType) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	if err := e.EncodeToken(start); err != nil {
		return err
	}
	for _, w := range h.Words {
		if err := e.EncodeElement(w, xml.StartElement{Name: xml.Name{Local: "w"}}); err != nil {
			return err
		}
	}
	if err := e.EncodeToken(start.End()); err != nil {
		return err
	}
	return e.Flush()
}

func (h *hookType) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			var s string
			if err := d.DecodeElement(&s, &t); err != nil {
				return err
			}
			h.Words = append(h.Words, s)
		case xml.EndElement:
			return nil
		}
	}
}

func TestV2HookTypeIdentity(t *testing.T) {
	type doc struct {
		XMLName xml.Name `xml:"doc"`
		H       hookType `xml:"h"`
	}
	in := doc{H: hookType{Words: []string{"a", "b&c"}}}
	out, err := xml.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	const want = `<doc><h><w>a</w><w>b&amp;c</w></h></doc>`
	if string(out) != want {
		t.Fatalf("Marshal = %s, want %s", out, want)
	}
	var got doc
	if err := xml.NewDecoder(strings.NewReader(want)).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.H.Words, ",") != "a,b&c" {
		t.Fatalf("Unmarshal words = %q", got.H.Words)
	}
	// Also through the package-level function and a user-created Encoder.
	var got2 doc
	if err := xml.Unmarshal([]byte(want), &got2); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	enc := xml.NewEncoder(&sb)
	if err := enc.Encode(in); err != nil {
		t.Fatal(err)
	}
	if sb.String() != want {
		t.Fatalf("Encoder.Encode = %s", sb.String())
	}
}
