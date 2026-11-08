// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// scanBenchText returns n bytes of character data of the given shape.
func scanBenchText(shape string, n int) string {
	fill := func(unit string) string {
		s := strings.Repeat(unit, n/len(unit)+1)
		s = s[:n]
		for !utf8.ValidString(s) {
			s = s[:len(s)-1] // do not cut a UTF-8 sequence
		}
		return s
	}
	plain := "The quick brown fox jumps over the lazy dog. "
	switch shape {
	case "plain":
		return fill(plain)
	case "entFront": // a reference at the start of the run
		return "&amp;" + fill(plain)[min(5, n):]
	case "entEnd": // a reference just before the closing '<'
		return fill(plain)[:max(n-5, 0)] + "&amp;"
	case "entities": // a reference every 8 bytes
		return fill("abc&amp;")
	case "korean":
		return fill("가나다라마바사 ")
	case "mixed": // Korean words in ASCII text
		return fill("data 가나다 value ")
	}
	panic(shape)
}

// scanBenchDoc wraps many copies of a text run in elements, about 256 KiB in
// all, so the run's length decides the cost per token.
func scanBenchDoc(shape string, n int) []byte {
	text := scanBenchText(shape, n)
	k := max((256<<10)/(len(text)+7), 1)
	var b strings.Builder
	b.WriteString("<r>")
	for range k {
		b.WriteString("<p>")
		b.WriteString(text)
		b.WriteString("</p>")
	}
	b.WriteString("</r>")
	return []byte(b.String())
}

// BenchmarkTextScan measures text runs of one shape and size read with Text,
// through both inputs and both profiles (the permissive one is what
// encoding/xml uses).
func BenchmarkTextScan(b *testing.B) {
	for _, shape := range []string{"plain", "entFront", "entEnd", "entities", "korean", "mixed"} {
		for _, n := range []int{16, 64, 256, 4 << 10, 32 << 10} {
			doc := scanBenchDoc(shape, n)
			for _, prof := range []string{"strict", "perm"} {
				var opts []Options
				if prof == "perm" {
					opts = []Options{Permissive(true)}
				}
				read := func(b *testing.B, d *Decoder) {
					sum := 0
					for {
						t, err := d.ReadToken()
						if err != nil {
							if err.Error() != "EOF" {
								b.Fatal(err)
							}
							break
						}
						if t.Kind() == Text {
							sum += len(t.Text())
						}
					}
					_ = sum
				}
				name := fmt.Sprintf("%s/%d/%s", shape, n, prof)
				b.Run(name+"/bytes", func(b *testing.B) {
					b.SetBytes(int64(len(doc)))
					b.ReportAllocs()
					d := NewDecoderBytes(doc, opts...)
					for b.Loop() {
						d.ResetBytes(doc, opts...)
						read(b, d)
					}
				})
				b.Run(name+"/reader", func(b *testing.B) {
					b.SetBytes(int64(len(doc)))
					b.ReportAllocs()
					d := NewDecoder(bytes.NewReader(doc), opts...)
					for b.Loop() {
						d.Reset(bytes.NewReader(doc), opts...)
						read(b, d)
					}
				})
			}
		}
	}
}

// BenchmarkCommentedDoc is TagHeavy with a comment after every few elements:
// in the default profile, every token the direct path cannot read sends the
// next batch of tokens through the boundary engine.
func BenchmarkCommentedDoc(b *testing.B) {
	unit := `<item id="1" name="abc"><v>some text here</v><v>more &amp; text</v></item>` + "\n"
	doc := []byte("<r>" + strings.Repeat(unit+unit+"<!-- c -->\n", 8000) + "</r>")
	for _, in := range []string{"bytes", "reader"} {
		b.Run(in, func(b *testing.B) {
			b.SetBytes(int64(len(doc)))
			b.ReportAllocs()
			d := NewDecoderBytes(doc)
			for b.Loop() {
				if in == "bytes" {
					d.ResetBytes(doc)
				} else {
					d.Reset(bytes.NewReader(doc))
				}
				for {
					t, err := d.ReadToken()
					if err != nil {
						break
					}
					if t.Kind() == Text {
						_ = t.Text()
					}
				}
			}
		})
	}
}
