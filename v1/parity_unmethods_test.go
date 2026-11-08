// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"reflect"
	"strings"
	"testing"
)

// Unmarshal finds UnmarshalXML, UnmarshalXMLAttr and UnmarshalText on
// values of every shape it fills. These held for the
// standard library when imported.

type methXML struct{ S string }

func (m *methXML) UnmarshalXML(d *Decoder, start StartElement) error {
	var s string
	if err := d.DecodeElement(&s, &start); err != nil {
		return err
	}
	m.S = "xml:" + s
	return nil
}

type methText string

func (m *methText) UnmarshalText(b []byte) error {
	*m = methText("text:" + string(b))
	return nil
}

type methAttr struct{ S string }

func (m *methAttr) UnmarshalXMLAttr(a Attr) error {
	m.S = "attr:" + a.Value
	return nil
}

// methValue has its method on the value receiver.
type methValue struct{ p *string }

func (m methValue) UnmarshalText(b []byte) error {
	*m.p = "value:" + string(b)
	return nil
}

type methDoc struct {
	X      methXML     `xml:"x"`
	PX     *methXML    `xml:"px"`
	SX     []methXML   `xml:"sx"`
	SPX    []*methXML  `xml:"spx"`
	SSX    [][]methXML `xml:"ssx"`
	IX     any         `xml:"ix"`
	T      methText    `xml:"t"`
	ST     []methText  `xml:"st"`
	V      methValue   `xml:"v"`
	A      methAttr    `xml:"a,attr"`
	PA     *methAttr   `xml:"pa,attr"`
	SA     []methAttr  `xml:"sa,attr"`
	TA     methText    `xml:"ta,attr"`
	STA    []methText  `xml:"sta,attr"`
	Data   methText    `xml:",chardata"`
	Plain  string      `xml:"plain"`
	Nested struct {
		T methText `xml:"t"`
	} `xml:"n"`
}

func TestParityUnmarshalMethods(t *testing.T) {
	in := `<doc a="1" pa="2" sa="3" ta="4" sta="5">d` +
		`<x>x</x><px>px</px><sx>s1</sx><sx>s2</sx><spx>p1</spx><ssx>q</ssx><ix>i</ix>` +
		`<t>t</t><st>u1</st><st>u2</st><v>v</v><plain>p</plain><n><t>nt</t></n>` +
		`</doc>`
	var vs string
	got := methDoc{IX: &methXML{}, V: methValue{&vs}}
	if err := Unmarshal([]byte(in), &got); err != nil {
		t.Fatal(err)
	}
	want := methDoc{
		X:     methXML{"xml:x"},
		PX:    &methXML{"xml:px"},
		SX:    []methXML{{"xml:s1"}, {"xml:s2"}},
		SPX:   []*methXML{{"xml:p1"}},
		SSX:   [][]methXML{{{"xml:q"}}},
		IX:    &methXML{"xml:i"},
		T:     "text:t",
		ST:    []methText{"text:u1", "text:u2"},
		V:     got.V,
		A:     methAttr{"attr:1"},
		PA:    &methAttr{"attr:2"},
		SA:    []methAttr{{"attr:3"}},
		TA:    "text:4",
		STA:   []methText{"text:5"},
		Data:  "text:d",
		Plain: "p",
	}
	want.Nested.T = "text:nt"
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
	if vs != "value:v" {
		t.Errorf("value receiver: got %q", vs)
	}

	// The same types at the top level.
	var x methXML
	var tx methText
	var sx []methXML
	for _, c := range []struct {
		v    any
		want any
	}{
		{&x, &methXML{"xml:top"}},
		{&tx, func() *methText { s := methText("text:top"); return &s }()},
		{&sx, &[]methXML{{"xml:top"}}},
	} {
		if err := NewDecoder(strings.NewReader(`<r>top</r>`)).Decode(c.v); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(c.v, c.want) {
			t.Errorf("top level %T: got %+v, want %+v", c.v, c.v, c.want)
		}
	}
}
