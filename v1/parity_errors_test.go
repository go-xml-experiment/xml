// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"strings"
	"testing"
)

// TestParityTruncatedAttrError pins the error for an attribute value cut off
// by the end of input. It held for the standard library when imported.
func TestParityTruncatedAttrError(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		// A reference that ends before the end of input is judged first.
		{`<a b="& x`, "invalid character entity & (no semicolon)"},
		{`<a b="&#1 x`, "invalid character entity &#1 (no semicolon)"},
		{`<a b="&lt;&bad; x`, "invalid character entity &bad;"},
		{`<a b="x" c="&a&b`, "invalid character entity &a (no semicolon)"},
		// One that runs to the end of input is not.
		{`<a b="&lt;&am`, "unexpected EOF"},
		{`<a b="&#12`, "unexpected EOF"},
	} {
		d := NewDecoder(strings.NewReader(tt.in))
		var err error
		for err == nil {
			_, err = d.Token()
		}
		if se, ok := err.(*SyntaxError); !ok || se.Msg != tt.want {
			t.Errorf("Token(%q) error = %v, want %q", tt.in, err, tt.want)
		}
	}
}
