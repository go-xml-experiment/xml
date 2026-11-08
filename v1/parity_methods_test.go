// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"strings"
	"testing"
)

// These cases pin down how Marshal finds marshaling methods. They held for the
// standard library when imported.

type valText struct{ S string }

func (v valText) MarshalText() ([]byte, error) { return []byte("val:" + v.S), nil }

type ptrText struct{ S string }

func (v *ptrText) MarshalText() ([]byte, error) { return []byte("ptr:" + v.S), nil }

type nilAttr struct{}

func (*nilAttr) MarshalXMLAttr(name Name) (Attr, error) {
	return Attr{Name: name, Value: "nil-receiver"}, nil
}

type rawString string

type methodsDoc struct {
	XMLName Name     `xml:"doc"`
	VA      valText  `xml:"va,attr"`
	PA      ptrText  `xml:"pa,attr"`
	NA      *nilAttr `xml:"na,attr"`
	VE      valText  `xml:"ve"`
	PE      ptrText  `xml:"pe"`
	VC      valText  `xml:",chardata"`
	N       float64  `xml:"n"`
	Deep    string   `xml:"a>b>c"`
	Deeper  string   `xml:"a>b>d"`
	Other   string   `xml:"a>e"`
}

type valDoc struct {
	XMLName Name    `xml:"doc"`
	A       valText `xml:"a,attr"`
	C       valText `xml:",chardata"`
	E       valText `xml:"e"`
}

type innerDoc struct {
	XMLName Name      `xml:"doc"`
	Raw     rawString `xml:",innerxml"`
}

func TestMarshalMethodReceivers(t *testing.T) {
	v := methodsDoc{
		VA: valText{"1"}, PA: ptrText{"2"},
		VE: valText{"3"}, PE: ptrText{"4"}, VC: valText{"5"},
		N: 1.5, Deep: "x", Deeper: "y", Other: "z",
	}
	for _, tt := range []struct {
		name string
		in   any
		want string // or the error text
	}{
		// An addressable value can use pointer methods; a nil pointer
		// with a method on *T is still called.
		{"pointer", &v, `<doc va="val:1" pa="ptr:2" na="nil-receiver"><ve>val:3</ve><pe>ptr:4</pe>val:5<n>1.5</n><a><b><c>x</c><d>y</d></b><e>z</e></a></doc>`},
		// A copy is not addressable, so the method on *ptrText is not
		// used and the struct cannot be an attribute.
		{"value", v, `xml: unsupported type: xml.ptrText`},
		{"value receivers only", valDoc{A: valText{"8"}, E: valText{"9"}, C: valText{"10"}}, `<doc a="val:8">val:10<e>val:9</e></doc>`},
		{"top-level value", valText{"6"}, `<valText>val:6</valText>`},
		{"top-level pointer", &ptrText{"7"}, `<ptrText>ptr:7</ptrText>`},
		// Only the exact types string and []byte are written verbatim; a
		// named string type is marshaled as an element.
		{"named innerxml", innerDoc{Raw: "<x/>"}, `<doc><Raw>&lt;x/&gt;</Raw></doc>`},
	} {
		got, err := Marshal(tt.in)
		if err != nil {
			got = []byte(err.Error())
		}
		if string(got) != tt.want {
			t.Errorf("%s:\n got %s\nwant %s", tt.name, got, tt.want)
		}
	}
}

// A value is not changed by marshaling, and repeated calls through the
// pooled scratch buffers give the same output.
func TestMarshalScratchReuse(t *testing.T) {
	type item struct {
		A float64 `xml:"a,attr"`
		B valText `xml:"b,attr"`
		C int     `xml:"c"`
	}
	type list struct {
		XMLName Name   `xml:"l"`
		Items   []item `xml:"i"`
	}
	var l list
	var want strings.Builder
	want.WriteString("<l>")
	for i := range 50 {
		l.Items = append(l.Items, item{A: float64(i) + 0.25, B: valText{strings.Repeat("b", i)}, C: i})
		want.WriteString(`<i a="` + formatQuarter(i) + `" b="val:` + strings.Repeat("b", i) + `"><c>` + itoa(i) + `</c></i>`)
	}
	want.WriteString("</l>")
	for range 3 {
		got, err := Marshal(&l)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want.String() {
			t.Fatalf("\n got %s\nwant %s", got, want.String())
		}
	}
}

func itoa(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return itoa(i/10) + string(rune('0'+i%10))
}

func formatQuarter(i int) string { return itoa(i) + ".25" }
