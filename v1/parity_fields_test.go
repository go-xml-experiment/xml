// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"errors"
	"strings"
	"testing"
)

// These cases pin down field rules and error behavior that marshaling and
// unmarshaling must keep. They held for the standard library when imported.

type parityInt int

type parityText struct {
	XMLName struct{} `xml:"nm"`
}

func (parityText) MarshalText() ([]byte, error) { return []byte("v"), nil }

func TestParityFieldRules(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   any
		want string
	}{{
		// omitzero is not an option of this package, so it is ignored
		// even where omitempty would be invalid.
		"chardata,omitzero",
		&struct {
			XMLName struct{} `xml:"t"`
			C       string   `xml:",chardata,omitzero"`
		}{C: "x"},
		`<t>x</t>`,
	}, {
		// An unexported embedded non-struct is an element.
		"unexported embedded",
		&struct {
			XMLName struct{} `xml:"t"`
			parityInt
		}{parityInt: 3},
		`<t><parityInt>3</parityInt></t>`,
	}, {
		// An untagged attribute is named by its type's XMLName.
		"attr named by XMLName",
		&struct {
			XMLName struct{}   `xml:"t"`
			A       parityText `xml:",attr"`
		}{},
		`<t nm="v"></t>`,
	}} {
		got, err := Marshal(tt.in)
		if err != nil || string(got) != tt.want {
			t.Errorf("%s: Marshal = %s, %v; want %s", tt.name, got, err, tt.want)
		}
	}
}

type failingAttr struct{}

func (failingAttr) MarshalXMLAttr(Name) (Attr, error) { return Attr{}, errors.New("attr failed") }

// An error in an attribute leaves nothing of the element, and the Encoder
// goes on with the next value.
func TestParityAttrErrorLeavesNoTag(t *testing.T) {
	var b strings.Builder
	enc := NewEncoder(&b)
	bad := struct {
		XMLName struct{}    `xml:"bad"`
		A       failingAttr `xml:"a,attr"`
	}{}
	if err := enc.Encode(&bad); err == nil || err.Error() != "attr failed" {
		t.Fatalf("Encode(bad) = %v", err)
	}
	good := struct {
		XMLName struct{} `xml:"good"`
	}{}
	if err := enc.Encode(&good); err != nil {
		t.Fatalf("Encode(good) = %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("Close = %v", err)
	}
	if got := b.String(); got != `<good></good>` {
		t.Errorf("output %q, want %q", got, `<good></good>`)
	}
}
