// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"testing"
)

// FuzzRoundtrip checks that unmarshaling arbitrary input does not panic,
// and that what it produces marshals and unmarshals back to itself.
func FuzzRoundtrip(f *testing.F) {
	for _, s := range []string{
		`<root xmlns="urn:n" id="x"><items><item n="1">one</item><note>n</note></items>text<!--c--><ptr n="2"/><e1>e</e1></root>`,
		`<root id="&amp;&lt;"><when>2026-01-02T03:04:05Z</when></root>`,
		`<s i8="7" f32="0.25"><b>1</b><i>-3</i><u>9</u><f>1e3</f><s><![CDATA[x]]></s><bytes>raw</bytes><p>4</p></s>`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		check := func(v, w any) {
			if err := Unmarshal(in, v); err != nil {
				return
			}
			b, err := Marshal(v)
			if err != nil {
				return // e.g. a name that is not valid XML
			}
			if err := Unmarshal(b, w); err != nil {
				t.Fatalf("Unmarshal of Marshal output %s: %v", b, err)
			}
			b2, err := Marshal(w)
			if err != nil || string(b2) != string(b) {
				t.Fatalf("second Marshal:\n%s\n%s, %v", b, b2, err)
			}
		}
		check(new(nested), new(nested))
		check(new(scalars), new(scalars))
		var v1, v2 struct {
			Inner string `xml:",innerxml"`
		}
		check(&v1, &v2)
	})
}
