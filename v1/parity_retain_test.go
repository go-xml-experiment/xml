// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bufio"
	"bytes"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
)

// Attribute values that Unmarshal stores, or hands to methods that may
// keep them, must not change when the decoder reads on or the caller
// reuses its input. These held for the standard library when imported.

type retainAttr struct{ A Attr }

func (r *retainAttr) UnmarshalXMLAttr(a Attr) error {
	r.A = a
	return nil
}

type retainText struct{ B []byte }

func (r *retainText) UnmarshalText(b []byte) error {
	r.B = b // kept without a copy
	return nil
}

type retainStart struct{ Attr []Attr }

func (r *retainStart) UnmarshalXML(d *Decoder, start StartElement) error {
	r.Attr = start.Attr
	return d.Skip()
}

type retainItem struct {
	S    string     `xml:"s,attr"`
	P    *string    `xml:"p,attr"`
	Attr Attr       `xml:"a,attr"`
	M    retainAttr `xml:"m,attr"`
	T    retainText `xml:"t,attr"`
	B    []byte     `xml:"b,attr"`
	N    int        `xml:"n,attr"`
	Rest []Attr     `xml:",any,attr"`
}

type retainDoc struct {
	Items []retainItem `xml:"item"`
	Start retainStart  `xml:"start"`
	Fill  []string     `xml:"fill"`
}

func retainInput() []byte {
	var sb strings.Builder
	sb.WriteString("<doc>")
	for i := range 3 {
		v := string(rune('a' + i))
		sb.WriteString(`<item s="s` + v + `" p="p` + v + `" a="a` + v + `" m="m` + v + `" t="t` + v +
			`" b="b` + v + `" n="7" x="x` + v + `" ns:y="y` + v + `" xmlns:ns="urn:` + v + `"/>`)
	}
	sb.WriteString(`<start k="k" xmlns:q="urn:q" q:j="j"><in/></start>`)
	for range 200 {
		sb.WriteString(`<fill>` + strings.Repeat("Z", 40) + `</fill>`)
	}
	sb.WriteString("</doc>")
	return []byte(sb.String())
}

func retainWant() retainDoc {
	var d retainDoc
	for i := range 3 {
		v := string(rune('a' + i))
		p := "p" + v
		d.Items = append(d.Items, retainItem{
			S:    "s" + v,
			P:    &p,
			Attr: Attr{Name{Local: "a"}, "a" + v},
			M:    retainAttr{Attr{Name{Local: "m"}, "m" + v}},
			T:    retainText{[]byte("t" + v)},
			B:    []byte("b" + v),
			N:    7,
			Rest: []Attr{
				{Name{Local: "x"}, "x" + v},
				{Name{Space: "urn:" + v, Local: "y"}, "y" + v},
				{Name{Space: "xmlns", Local: "ns"}, "urn:" + v},
			},
		})
	}
	d.Start.Attr = []Attr{
		{Name{Local: "k"}, "k"},
		{Name{Space: "xmlns", Local: "q"}, "urn:q"},
		{Name{Space: "urn:q", Local: "j"}, "j"},
	}
	for range 200 {
		d.Fill = append(d.Fill, strings.Repeat("Z", 40))
	}
	return d
}

func TestParityRetainedAttrs(t *testing.T) {
	want := retainWant()
	check := func(name string, got retainDoc, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s:\ngot  %+v\nwant %+v", name, got.Items, want.Items)
			if !reflect.DeepEqual(got.Start, want.Start) {
				t.Errorf("%s: start attrs %+v, want %+v", name, got.Start, want.Start)
			}
		}
	}

	in := retainInput()
	var got retainDoc
	err := Unmarshal(in, &got)
	for i := range in {
		in[i] = '#' // the caller reuses its input
	}
	check("Unmarshal", got, err)

	for _, r := range []struct {
		name string
		new  func([]byte) *Decoder
	}{
		{"bytes.Buffer", func(b []byte) *Decoder { return NewDecoder(bytes.NewBuffer(b)) }},
		{"bytes.Reader", func(b []byte) *Decoder { return NewDecoder(bytes.NewReader(b)) }},
		{"strings.Reader", func(b []byte) *Decoder { return NewDecoder(strings.NewReader(string(b))) }},
		{"bufio.Reader", func(b []byte) *Decoder { return NewDecoder(bufio.NewReaderSize(bytes.NewReader(b), 16)) }},
		{"OneByteReader", func(b []byte) *Decoder { return NewDecoder(iotest.OneByteReader(bytes.NewReader(b))) }},
	} {
		var got retainDoc
		err := r.new(retainInput()).Decode(&got)
		check(r.name, got, err)
	}
}

// TestParityAutoCloseAttrs decodes an element that follows one AutoClose
// closes, so that its start waits while the invented end goes first.
func TestParityAutoCloseAttrs(t *testing.T) {
	type x struct {
		A    string `xml:"urn:u a,attr"`
		B    Attr   `xml:"b,attr"`
		Rest []Attr `xml:",any,attr"`
	}
	type doc struct {
		Br []string `xml:"br"`
		X  x        `xml:"x"`
	}
	d := NewDecoder(strings.NewReader(`<doc><br><x xmlns:p="urn:u" p:a="1" b="2" c="3"></x>` + strings.Repeat("<br>", 50) + `</doc>`))
	d.Strict = false
	d.AutoClose = []string{"br"}
	var got doc
	if err := d.Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := x{
		A: "1",
		B: Attr{Name{Local: "b"}, "2"},
		Rest: []Attr{
			{Name{Space: "xmlns", Local: "p"}, "urn:u"},
			{Name{Local: "c"}, "3"},
		},
	}
	if !reflect.DeepEqual(got.X, want) || len(got.Br) != 51 {
		t.Errorf("got %+v, %d br; want %+v, 51 br", got.X, len(got.Br), want)
	}
}
