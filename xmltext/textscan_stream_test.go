// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// splitReader returns s[:at] on its first Read and the rest on the next.
type splitReader struct {
	s  string
	at int
}

func (r *splitReader) Read(p []byte) (int, error) {
	if len(r.s) == 0 {
		return 0, io.EOF
	}
	k := len(r.s)
	if r.at > 0 {
		k, r.at = r.at, 0
	}
	k = min(k, len(p))
	copy(p, r.s[:k])
	r.s = r.s[k:]
	return k, nil
}

// traceWithPos is traceTokens with the error's offset, line and column.
func traceWithPos(d *Decoder) string {
	s := traceTokens(d)
	if _, err := d.ReadToken(); err != nil {
		var se *SyntaxError
		if errors.As(err, &se) {
			s += fmt.Sprintf("at offset=%d line=%d col=%d\n", se.Offset, se.Line, se.Column)
		}
	}
	return s
}

// scanSplitDocs are documents whose text runs put the scanner's resume state
// to work when the input is cut inside them: references, "]]>", UTF-8
// sequences, CRLF and problems near a cut.
var scanSplitDocs = []string{
	"<root>hello &amp; world</root>",
	"<root>hello &custom; world</root>",
	"<root>&#x1F600;&#65; and &#xAC00;</root>",
	"<root>abc]]></root>",
	"<root>abc]]</root>",
	"<root>abc]]]x]]</root>",
	"<root>abc]]<",
	"<root>abc]<",
	"<root>abc]]",
	"<root>ab\xe4\xb8",
	"<root>가나다라 😀 𝄞 é</root>",
	"<root>a\r\nb\rc\r</root>",
	"<root>ab\xe4\xb8</root>",
	"<root>ab\xed\xa0\x80cd</root>",
	"<root>ab\xef\xbf\xbe</root>",
	"<root>a\x01b</root>",
	"<root>&bogus; x</root>",
	"<root>a &am</root>",
	"<root a=\"x>y\" b='&amp;>'>t&gt;</root>",
	"<root>" + strings.Repeat("plain text ", 6) + "&lt;" + strings.Repeat("가", 5) + "</root>",
	"<root>" + strings.Repeat("y", 70) + "]]>" + "</root>",
	"<root>t</root>  \n",
	"<root>t</root>x",
	"<root/>\xff",
	"<root>x<!--c-->tail &amp; more</root>",
}

var scanSplitOptions = [][]Options{
	nil,
	{Permissive(true)},
	{Permissive(true), Loose(true)},
	{WithEntities(map[string]string{"custom": "C<>"})},
	{AllowUnknownEntities(true)},
	{SplitText(true), MaxTokenSize(8)},
	{MaxTokenSize(12)},
}

// TestTextSplitAtEveryByte reads each document cut into two reads at every
// byte, and one byte at a time, and requires the bytes input's tokens,
// offsets and error positions, with the direct path on and off.
func TestTextSplitAtEveryByte(t *testing.T) {
	for _, doc := range scanSplitDocs {
		for _, opts := range scanSplitOptions {
			if !strings.Contains(doc, "&") {
				// Without references no text can hold '<': it ends a run.
				toks, _ := record(NewDecoderBytes([]byte(doc), opts...))
				for _, r := range toks {
					if r.kind == Text && strings.Contains(r.s, "<") {
						t.Fatalf("doc %q options %v: text token %q holds '<'", doc, opts, r.s)
					}
				}
			}
			want := traceWithPos(NewDecoderBytes([]byte(doc), opts...))
			check := func(name string, d *Decoder) {
				t.Helper()
				if got := traceWithPos(d); got != want {
					t.Fatalf("doc %q options %v %s:\ngot:\n%s\nwant (bytes):\n%s", doc, opts, name, got, want)
				}
			}
			check("one-byte reader", NewDecoder(iotest.OneByteReader(strings.NewReader(doc)), opts...))
			for at := 1; at < len(doc); at++ {
				check(fmt.Sprintf("cut at %d", at), NewDecoder(&splitReader{doc, at}, opts...))
				off := NewDecoder(&splitReader{doc, at}, opts...)
				off.direct = false
				check(fmt.Sprintf("cut at %d, no direct path", at), off)
			}
		}
	}
}
