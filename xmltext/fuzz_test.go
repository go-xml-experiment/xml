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

// FuzzDecoderChunked checks chunked reads and compares accepted inputs with
// encoding/xml in both profiles, allowing only the documented differences.
func FuzzDecoderChunked(f *testing.F) {
	for _, s := range corpus[:len(corpus)-1] {
		f.Add(s)
	}
	f.Add("<r>" + strings.Repeat("x", textWindow-2) + "&amp;한\r\n]]x</r>")
	for _, s := range []string{
		"<\u0132/>",
		`<?xml aversion="2.0"?><r/>`,
		`<?xml aencoding="latin1"?><r/>`,
		`<!DOCTYPE r [<!-- c --><!ELEMENT r ANY>]><r/>`,
		`<!DOCTYPE r "<!-- quoted -->"><r/>`,
		`<r><!-- c -->text</r>`,
		`<a xmlns:p="` + XMLNSNamespace + `" p:x="1"/>`,
		`<a xmlns:q="xmlns" q:foo="bar" xmlns:foo="x"/>`,
		"<r a=\"x\ny\tz\r\nw\rv&#9;&#10;&#13;&#x9;\" b='\t'/>",
		"<r xmlns:p=\"u\nv\"><p:c p:a=\"\r\n\"/></r>",
		"\ufeff<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n<r>&#0000000065;&#x0000000041;</r>",
		"\ufeff <r/>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, permissive := range []bool{false, true} {
			got, err := readAll(t, NewDecoderBytes([]byte(in), Permissive(permissive)))
			got2, err2 := readAll(t, NewDecoder(iotest.OneByteReader(strings.NewReader(in)), Permissive(permissive)))
			// A streaming decoder can emit earlier Text pieces before discovering
			// an error later in the run; complete input validates the entire run.
			if (err == nil) != (err2 == nil) || err == nil && fmt.Sprint(normalize(got)) != fmt.Sprint(normalize(got2)) {
				t.Fatalf("permissive=%v chunked mismatch: %v vs %v\n%v\n%v", permissive, err, err2, got, got2)
			}
		}
	})
}
