// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"reflect"
	"strings"
	"testing"
)

// A tag without a namespace names an element or attribute in no namespace,
// as in Namespaces in XML 1.0; it does not match the same local name in
// some other namespace (cf. GO-2026-6909, where encoding/xml filled a
// SAML field from an element in an attacker's namespace).
func TestStrictNames(t *testing.T) {
	type subject struct {
		XMLName Name   `xml:"urn:oasis:names:tc:SAML:2.0:assertion Subject"`
		NameID  string `xml:"urn:oasis:names:tc:SAML:2.0:assertion NameID"`
		Bare    string `xml:"Bare"`
		ID      string `xml:"ID,attr"`
		Chain   string `xml:"a>b"`
	}
	const saml = `xmlns="urn:oasis:names:tc:SAML:2.0:assertion" xmlns:e="urn:evil"`
	tests := []struct {
		name string
		in   string
		want subject
	}{{
		name: "Qualified",
		in:   `<Subject ` + saml + `><NameID>alice</NameID></Subject>`,
		want: subject{NameID: "alice"},
	}, {
		name: "ElementInOtherNamespace",
		in:   `<Subject ` + saml + `><e:NameID>mallory</e:NameID><NameID>alice</NameID></Subject>`,
		want: subject{NameID: "alice"},
	}, {
		name: "BareTagIsNoNamespace",
		in:   `<Subject ` + saml + `><Bare>in default</Bare><e:Bare>in evil</e:Bare><Bare xmlns="">plain</Bare></Subject>`,
		want: subject{Bare: "plain"},
	}, {
		name: "AttrInOtherNamespace",
		in:   `<Subject ` + saml + ` e:ID="mallory"/>`,
		want: subject{},
	}, {
		name: "AttrInNoNamespace",
		in:   `<Subject ` + saml + ` ID="alice"/>`,
		want: subject{ID: "alice"},
	}, {
		name: "ParentInOtherNamespace",
		in:   `<Subject ` + saml + `><a><b>default</b></a><e:a><b xmlns="">evil</b></e:a><a xmlns=""><b>plain</b></a></Subject>`,
		want: subject{Chain: "plain"},
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got subject
			if err := Unmarshal([]byte(tt.in), &got); err != nil {
				t.Fatal(err)
			}
			got.XMLName = Name{}
			if got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestStrictRootName(t *testing.T) {
	type root struct {
		XMLName Name `xml:"root"`
	}
	err := Unmarshal([]byte(`<root xmlns="urn:x"/>`), new(root))
	if err == nil || !strings.Contains(err.Error(), "expected element <root> in no namespace but have name space urn:x") {
		t.Fatalf("Unmarshal: %v", err)
	}
	type anyRoot struct {
		XMLName Name // no tag: any element
	}
	var v anyRoot
	if err := Unmarshal([]byte(`<p:r xmlns:p="urn:x"/>`), &v); err != nil || v.XMLName != (Name{Space: "urn:x", Local: "r"}) {
		t.Fatalf("Unmarshal into untagged XMLName: %+v, %v", v.XMLName, err)
	}
}

// A field whose tag has no namespace takes the name of its type's XMLName
// tag, namespace included, as Marshal writes it.
func TestStrictNameFromType(t *testing.T) {
	type own struct {
		XMLName struct{} `xml:"urn:own own"`
		Leaf    string   `xml:"leaf"`
	}
	type root struct {
		XMLName struct{} `xml:"root"`
		Own     *own     `xml:"own"`
	}
	v := root{Own: &own{Leaf: "l"}}
	out, err := Marshal(&v)
	if err != nil {
		t.Fatal(err)
	}
	if want := `<root><ns1:own xmlns:ns1="urn:own"><leaf>l</leaf></ns1:own></root>`; string(out) != want {
		t.Fatalf("Marshal:\n got %s\nwant %s", out, want)
	}
	var got root
	if err := Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got.Own == nil || got.Own.Leaf != "l" {
		t.Fatalf("Unmarshal(%s): %+v", out, got.Own)
	}
	got = root{}
	if err := Unmarshal([]byte(`<root><own><leaf>l</leaf></own></root>`), &got); err != nil || got.Own != nil {
		t.Fatalf("Unmarshal of <own> in no namespace: %+v, %v", got.Own, err)
	}
}

// The type-name rule of TestStrictNameFromType holds for slice items and
// pointers to them, which Marshal writes with the item type's
// XMLName.
func TestStrictNameFromItemType(t *testing.T) {
	type own struct {
		XMLName struct{} `xml:"urn:own own"`
		Leaf    string   `xml:"leaf"`
	}
	type root struct {
		XMLName struct{} `xml:"root"`
		Owns    []own    `xml:"own"`
		Ptrs    []*own   `xml:"p>own"`
	}
	type qroot struct {
		XMLName struct{} `xml:"urn:n root,qualified"`
		Owns    []own    `xml:"own"`
	}
	for _, v := range []any{
		&root{Owns: []own{{Leaf: "1"}, {Leaf: "2"}}, Ptrs: []*own{{Leaf: "3"}}},
		&qroot{Owns: []own{{Leaf: "1"}, {Leaf: "2"}}},
	} {
		out, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		got := reflectNew(v)
		if err := Unmarshal(out, got); err != nil {
			t.Fatal(err)
		}
		out2, err := Marshal(got)
		if err != nil {
			t.Fatal(err)
		}
		if string(out2) != string(out) {
			t.Errorf("round trip of %T:\n got %s\nwant %s", v, out2, out)
		}
	}
}

// reflectNew returns a pointer to a new zero value of the type v points to.
func reflectNew(v any) any { return reflect.New(reflect.TypeOf(v).Elem()).Interface() }
