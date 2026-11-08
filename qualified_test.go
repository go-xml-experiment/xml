// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"testing"

	"github.com/go-xml-experiment/xml/xmltext"
)

// The types of TestQualified follow a JAXB package with
// @XmlSchema(namespace = "urn:n", elementFormDefault = QUALIFIED), whose
// output for the same values is
// <root id="x" xmlns="urn:n" xmlns:ns2="urn:other"><items>i</items>
// <child><leaf>l</leaf></child><ns2:other>o</ns2:other></root>.
type qChild struct {
	Leaf string `xml:"leaf,omitempty"`
}

type qRoot struct {
	XMLName struct{} `xml:"urn:n root,qualified"`
	ID      string   `xml:"id,attr,omitempty"`
	Items   string   `xml:"items,omitempty"`
	Child   *qChild  `xml:"child"`
	Chain   string   `xml:"a>b,omitempty"`
	Other   string   `xml:"urn:other other,omitempty"`
	Own     *qOwn    `xml:"own"`
}

// qOwn names its own namespace without qualified: its fields are in no
// namespace, as in a JAXB package with elementFormDefault UNQUALIFIED.
type qOwn struct {
	XMLName struct{} `xml:"urn:own own"`
	Leaf    string   `xml:"leaf"`
}

func TestQualifiedMarshal(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{{
		name: "JAXB",
		v:    &qRoot{ID: "x", Items: "i", Child: &qChild{Leaf: "l"}, Other: "o"},
		want: `<root xmlns="urn:n" id="x"><items>i</items><child><leaf>l</leaf></child><ns1:other xmlns:ns1="urn:other">o</ns1:other></root>`,
	}, {
		name: "Chain",
		v:    &qRoot{Chain: "c"},
		want: `<root xmlns="urn:n"><a><b>c</b></a></root>`,
	}, {
		name: "OwnNamespaceStopsInheritance",
		v:    &qRoot{Own: &qOwn{Leaf: "l"}},
		want: `<root xmlns="urn:n"><ns1:own xmlns:ns1="urn:own"><leaf xmlns="">l</leaf></ns1:own></root>`,
	}, {
		name: "Unqualified",
		v:    &qOwn{Leaf: "l"},
		want: `<ns1:own xmlns:ns1="urn:own"><leaf>l</leaf></ns1:own>`,
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := Marshal(tt.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(out) != tt.want {
				t.Fatalf("\n got %s\nwant %s", out, tt.want)
			}
		})
	}
}

func TestQualifiedUnmarshal(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want qRoot
	}{{
		name: "Default",
		in:   `<root xmlns="urn:n" id="x"><items>i</items><child><leaf>l</leaf></child></root>`,
		want: qRoot{ID: "x", Items: "i", Child: &qChild{Leaf: "l"}},
	}, {
		name: "Prefixed",
		in:   `<p:root xmlns:p="urn:n" xmlns:o="urn:other" id="x"><p:items>i</p:items><p:a><p:b>c</p:b></p:a><o:other>o</o:other></p:root>`,
		want: qRoot{ID: "x", Items: "i", Chain: "c", Other: "o"},
	}, {
		name: "NoNamespaceChild",
		in:   `<root xmlns="urn:n"><items xmlns="">i</items><a xmlns=""><b>c</b></a></root>`,
	}, {
		name: "OtherNamespaceChild",
		in:   `<root xmlns="urn:n"><items xmlns="urn:evil">i</items><child><leaf xmlns="urn:evil">l</leaf></child></root>`,
		want: qRoot{Child: &qChild{}},
	}, {
		name: "OwnNamespace",
		in:   `<root xmlns="urn:n"><own xmlns="urn:own"><leaf>default</leaf><leaf xmlns="">plain</leaf></own></root>`,
		want: qRoot{Own: &qOwn{Leaf: "plain"}},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got qRoot
			if err := Unmarshal([]byte(tt.in), &got); err != nil {
				t.Fatal(err)
			}
			if !qEqual(got, tt.want) {
				t.Fatalf("\n got %s\nwant %s", qString(got), qString(tt.want))
			}
		})
	}
}

// A qualified root keeps the declarations of a []Attr any,attr field,
// even one that binds the default namespace to another URI.
func TestQualifiedKeepsDeclarations(t *testing.T) {
	type root struct {
		XMLName struct{}       `xml:"urn:n root,qualified"`
		Attrs   []xmltext.Attr `xml:",any,attr"`
		Items   string         `xml:"items"`
	}
	const in = `<p:root xmlns:p="urn:n" xmlns="urn:z"><p:items>i</p:items></p:root>`
	var v root
	if err := Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if v.Items != "i" {
		t.Fatalf("Items = %q", v.Items)
	}
	out, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != in {
		t.Fatalf("\n got %s\nwant %s", out, in)
	}
}

func TestQualifiedTag(t *testing.T) {
	type bad struct {
		F string `xml:"f,qualified"`
	}
	if _, err := Marshal(bad{}); err == nil {
		t.Error("qualified on a field other than XMLName: no error")
	}
}

func qEqual(a, b qRoot) bool {
	return qString(a) == qString(b)
}

func qString(v qRoot) string {
	s := "id=" + v.ID + " items=" + v.Items + " chain=" + v.Chain + " other=" + v.Other
	if v.Child != nil {
		s += " child={leaf=" + v.Child.Leaf + "}"
	}
	if v.Own != nil {
		s += " own={leaf=" + v.Own.Leaf + "}"
	}
	return s
}
