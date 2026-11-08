// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// v1 keeps encoding/xml's behavior: no limit on a text run, a CDATA section or
// the attributes of a tag (xmltext's defaults of 16 MiB and 1000 do not apply).
func TestNoSizeLimits(t *testing.T) {
	var attrs strings.Builder
	for i := 0; i < 3000; i++ {
		fmt.Fprintf(&attrs, ` a%d="v"`, i)
	}
	big := strings.Repeat("x", 17<<20)
	for _, c := range []struct {
		name, in string
		check    func(Token) bool
	}{
		{"attributes", "<r" + attrs.String() + "/>", func(tk Token) bool { se, ok := tk.(StartElement); return !ok || len(se.Attr) == 3000 }},
		{"text", "<r>" + big + "</r>", func(tk Token) bool { cd, ok := tk.(CharData); return !ok || len(cd) == len(big) }},
		{"cdata", "<r><![CDATA[" + big + "]]></r>", func(tk Token) bool { cd, ok := tk.(CharData); return !ok || len(cd) == len(big) }},
	} {
		for _, reader := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/reader=%v", c.name, reader), func(t *testing.T) {
				var d *Decoder
				if reader {
					d = NewDecoder(struct{ io.Reader }{strings.NewReader(c.in)})
				} else {
					d = NewDecoder(strings.NewReader(c.in))
				}
				n := 0
				for {
					tk, err := d.RawToken()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if !c.check(tk) {
						t.Fatalf("token %d is wrong: %T", n, tk)
					}
					n++
				}
				if n != 3 && c.name != "attributes" || n != 1+1 && c.name == "attributes" {
					t.Fatalf("%d tokens", n)
				}
			})
		}
	}
}

// RawNesting leaves matching and its iterative stack to v1; xmltext's default
// depth limit must not constrain Token or RawToken.
func TestTokenNoDepthLimit(t *testing.T) {
	const depth = 20000
	for _, tag := range []string{"<a>", `<a x="y">`} {
		input := strings.Repeat(tag, depth) + strings.Repeat("</a>", depth)
		for _, raw := range []bool{false, true} {
			for _, buffered := range []bool{false, true} {
				t.Run(fmt.Sprintf("tag=%s/raw=%v/buffered=%v", tag, raw, buffered), func(t *testing.T) {
					var r io.Reader = strings.NewReader(input)
					if buffered {
						r = struct{ io.Reader }{r}
					}
					d := NewDecoder(r)
					next := d.Token
					if raw {
						next = d.RawToken
					}
					for i := range 2 * depth {
						token, err := next()
						if err != nil {
							t.Fatalf("token %d: %v", i, err)
						}
						if i < depth {
							if _, ok := token.(StartElement); !ok {
								t.Fatalf("token %d: %T, want StartElement", i, token)
							}
						} else if _, ok := token.(EndElement); !ok {
							t.Fatalf("token %d: %T, want EndElement", i, token)
						}
					}
					if token, err := next(); token != nil || err != io.EOF {
						t.Fatalf("end: %v, %v", token, err)
					}
				})
			}
		}
	}
}

// Fixed expectations avoid comparing the replacement against itself in-tree.
func TestCharDataBoundariesFixedExpectations(t *testing.T) {
	text := strings.Repeat("한글\r\n&amp;", 6000)
	for _, c := range []struct {
		input string
		want  []string
	}{
		{"<r>" + text + "</r>tail", []string{strings.Repeat("한글\n&", 6000), "tail"}},
		{"<r>before<![CDATA[" + text + "]]>after</r>", []string{"before", strings.Repeat("한글\n&amp;", 6000), "after"}},
		{"<r><![CDATA[]]><![CDATA[a]]>b&amp;c<![CDATA[d]]></r>", []string{"", "a", "b&c", "d"}},
	} {
		for _, raw := range []bool{false, true} {
			for _, buffered := range []bool{false, true} {
				var r io.Reader = strings.NewReader(c.input)
				if buffered {
					r = struct{ io.Reader }{r}
				}
				d := NewDecoder(r)
				next := d.Token
				if raw {
					next = d.RawToken
				}
				index := 0
				for {
					token, err := next()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if data, ok := token.(CharData); ok {
						if index >= len(c.want) || string(data) != c.want[index] {
							t.Fatalf("raw=%v buffered=%v CharData %d: %q", raw, buffered, index, data)
						}
						index++
					}
				}
				if index != len(c.want) {
					t.Fatalf("CharData count = %d, want %d", index, len(c.want))
				}
			}
		}
	}
}
