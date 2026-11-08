// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"fmt"
	"strings"
	"testing"
	"testing/iotest"
)

// manyPrefixed builds <r xmlns:p0="u" ... p0:a0="" ...> with n distinct local
// names; dup replaces the last attribute's local name by the first's, bound
// through a different prefix.
func manyPrefixed(n int, dup bool) string {
	var b strings.Builder
	b.WriteString("<r")
	for i := range n {
		fmt.Fprintf(&b, ` xmlns:p%d="u"`, i)
	}
	for i := range n {
		l := i
		if dup && i == n-1 {
			l = 0
		}
		fmt.Fprintf(&b, ` p%d:a%d="v"`, i, l)
	}
	b.WriteString("/>")
	return b.String()
}

// The default profile enforces the reserved prefixes and namespaces of
// Namespaces in XML 1.0, rejects names that are not QNames and attributes that
// expand to the same name; the permissive profiles keep encoding/xml's
// behavior on all of these.
func TestStrictNamespaceRules(t *testing.T) {
	const (
		xmlNS   = XMLNamespace
		xmlnsNS = XMLNSNamespace
	)
	for _, tc := range []struct {
		name, in string
		want     string // error substring in the default profile; "" if accepted
	}{
		{"xml bound elsewhere", `<r xmlns:xml="urn:evil" xml:lang="en"/>`, "prefix xml must be bound to"},
		{"xml bound to nothing", `<r xmlns:xml=""/>`, "prefix xml must be bound to"},
		{"xml bound to xml", `<r xmlns:xml="` + xmlNS + `" xml:lang="en"/>`, ""},
		{"xml namespace on other prefix", `<r xmlns:p="` + xmlNS + `"/>`, "can only be bound to prefix xml"},
		{"xml namespace as default", `<r xmlns="` + xmlNS + `"/>`, "can only be bound to prefix xml"},
		{"declare xmlns", `<r xmlns:xmlns="urn:evil"/>`, "prefix xmlns cannot be declared"},
		{"declare xmlns as xmlns namespace", `<r xmlns:xmlns="` + xmlnsNS + `"/>`, "prefix xmlns cannot be declared"},
		{"xmlns namespace on prefix", `<r xmlns:p="` + xmlnsNS + `"/>`, "cannot be bound"},
		{"xmlns namespace on prefix, used", `<r xmlns:p="` + xmlnsNS + `" p:x="1"/>`, "cannot be bound"},
		{"xmlns namespace as default", `<r xmlns="` + xmlnsNS + `"/>`, "cannot be bound"},
		{"xmlns namespace on child", `<a><b xmlns:p="` + xmlnsNS + `"/></a>`, "cannot be bound"},
		{"xmlns element prefix", `<xmlns:r/>`, "reserved prefix xmlns"},
		{"xmlns element prefix nested", `<a><xmlns:r xmlns:xmlns="urn:x"/></a>`, "reserved prefix xmlns"},
		{"empty prefixed namespace", `<r xmlns:p=""><p:c/></r>`, "empty namespace"},
		{"empty default namespace", `<r xmlns="u"><c xmlns=""/></r>`, ""},
		{"prefix with xml prefix text", `<r xmlns:xmlfoo="u" xmlns:xmlns2="v" xmlfoo:a="1"/>`, ""},
		{"same namespace twice", `<r xmlns:p="u" xmlns:q="u" p:x="1" q:x="2"/>`, "duplicate attribute q:x (same name as p:x)"},
		{"same namespace reversed", `<r xmlns:p="u" xmlns:q="u" q:x="1" p:x="2"/>`, "duplicate attribute p:x (same name as q:x)"},
		{"same namespace from ancestor", `<r xmlns:p="u"><c xmlns:q="u" p:x="1" q:x="2"/></r>`, "duplicate attribute q:x"},
		{"same namespace both from ancestors", `<r xmlns:p="u" xmlns:q="u"><c y="0" p:x="1" z="0" q:x="2"/></r>`, "duplicate attribute q:x"},
		{"different namespaces", `<r xmlns:p="u" xmlns:q="v" p:x="1" q:x="2"/>`, ""},
		{"same namespace different names", `<r xmlns:p="u" xmlns:q="u" p:x="1" q:y="2"/>`, ""},
		{"unprefixed beside prefixed", `<r xmlns:p="u" p:x="1" x="2"/>`, ""},
		{"rebound prefix in child", `<r xmlns:p="u" xmlns:q="u"><c xmlns:p="v" p:x="1" q:x="2"/></r>`, ""},
		{"rebound prefix collides", `<r xmlns:p="u" xmlns:q="v"><c xmlns:q="u" p:x="1" q:x="2"/></r>`, "duplicate attribute q:x"},
		{"many prefixed, distinct", manyPrefixed(40, false), ""},
		{"many prefixed, duplicate", manyPrefixed(40, true), "duplicate attribute p39:a0 (same name as p0:a0)"},
		{"few prefixed, duplicate", manyPrefixed(5, true), "duplicate attribute p4:a0 (same name as p0:a0)"},
		{"element name ends with colon", `<a:/>`, "name a: has an empty local name"},
		{"element name starts with colon", `<a><:b/></a>`, "name :b has an empty prefix"},
		{"attribute name ends with colon", `<a b:="1"/>`, "name b: has an empty local name"},
		{"attribute name starts with colon", `<a :b="1"/>`, "name :b has an empty prefix"},
		{"attribute name is a colon", `<A :=""/>`, "name : has an empty prefix"},
		{"local name starts with a digit", `<A xmlns:0="u"/>`, "name xmlns:0 has a local name that cannot start a name"},
		{"local name starts with a dot", `<a:.b xmlns:a="u"/>`, "has a local name that cannot start a name"},
		{"local name starts with a combining mark", "<r xmlns:a=\"u\" a:\u0300x=\"1\"/>", "has a local name that cannot start a name"},
		{"local name starts with a non-ASCII letter", "<r xmlns:a=\"u\" a:\u00e9x=\"1\"/>", ""},
		{"local name with digits and dots", `<r xmlns:a="u" a:x0.-1="1"/>`, ""},
		{"xml attributes", `<r xml:lang="en" xml:space="preserve"/>`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, stream := range []bool{false, true} {
				d := NewDecoderBytes([]byte(tc.in))
				if stream {
					d = NewDecoder(iotest.OneByteReader(strings.NewReader(tc.in)))
				}
				_, err := readAll2(d)
				switch {
				case tc.want == "" && err != nil:
					t.Errorf("stream=%v: unexpected error %v", stream, err)
				case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
					t.Errorf("stream=%v: error = %v, want substring %q", stream, err, tc.want)
				}
			}
			// Permissive and Loose parse every input exactly as encoding/xml does.
			for _, cfg := range []compatibilityCfg{strictCfg, looseCfg} {
				if _, err := readAll2(NewDecoderBytes([]byte(tc.in), cfg.options()...)); err != nil {
					t.Errorf("%+v: permissive profile rejects: %v", cfg, err)
				}
				if diff := diffExpected(t, tc.in, cfg); diff != "" {
					t.Errorf("%+v: %s", cfg, diff)
				}
			}
		})
	}
}
