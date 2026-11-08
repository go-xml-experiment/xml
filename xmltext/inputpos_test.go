// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func naiveInputPos(data []byte, off int) (line, col int) {
	line = 1 + bytes.Count(data[:off], []byte{'\n'})
	return line, off - (bytes.LastIndexByte(data[:off], '\n') + 1) + 1
}

func TestInputPosMatchesRecount(t *testing.T) {
	inputs := map[string]string{
		"lf":    "<r>\n<a x=\"1\">t\n\nu</a>\n<!-- c\n -->\n<![CDATA[a\nb]]>\n</r>\n",
		"crlf":  "<r>\r\n<a x=\"1\">t\r\n\r\nu</a>\r\n<?pi a\r\n?>\r\n</r>\r\n",
		"mixed": "<r\n a='1'\r\n b='2'>x\ry\n</r>",
		"long":  "<r>" + strings.Repeat("<i a=\"x\">hello\n&amp;\r\nworld</i>\n", 5000) + "</r>",
		"bigtx": "<r>" + strings.Repeat("line\n", 40000) + "</r>",
	}
	for name, in := range inputs {
		data := []byte(in)
		readers := map[string]func(*Decoder){
			"bytes":   func(d *Decoder) { d.ResetBytes(data) },
			"reader":  func(d *Decoder) { d.Reset(bytes.NewReader(data)) },
			"onebyte": func(d *Decoder) { d.Reset(iotest.OneByteReader(bytes.NewReader(data))) },
			"half":    func(d *Decoder) { d.Reset(iotest.HalfReader(bytes.NewReader(data))) },
		}
		for rn, reset := range readers {
			t.Run(name+"/"+rn, func(t *testing.T) {
				var d Decoder
				reset(&d)
				for n := 0; ; n++ {
					_, err := d.ReadToken()
					off := int(d.InputOffset())
					wl, wc := naiveInputPos(data, off)
					// Query twice and out of order: repeats and earlier
					// offsets must not disturb the memo.
					for range 2 {
						if l, c := d.InputPos(); l != wl || c != wc {
							t.Fatalf("token %d offset %d: InputPos = %d:%d, want %d:%d", n, off, l, c, wl, wc)
						}
					}
					if err == io.EOF {
						return
					}
					if err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

// Syntax errors may point before or after the offset InputPos last reported.
func TestInputPositionAnyOrder(t *testing.T) {
	data := []byte("<r>\n<a>\r\n</a>\n<b x=1>\n</b>\n\n</r>")
	d := NewDecoderBytes(data)
	for _, at := range []int{0, 9, 3, 12, 1, 30, 14, 14, 2, len(data), 5, 20, 0} {
		for _, ok := range []bool{false, true} {
			if ok { // advance d.pos so memoization applies to some of them
				d.ReadToken()
			}
			l, c := d.position(at)
			if wl, wc := naiveInputPos(data, at); l != wl || c != wc {
				t.Fatalf("position(%d) = %d:%d, want %d:%d", at, l, c, wl, wc)
			}
		}
	}
}

func TestInputPosAfterCharsetSwitch(t *testing.T) {
	in := "<?xml version=\"1.0\" encoding=\"x-test\"?>\n<r>\n<a/>\n</r>\n"
	var lines []int
	d := NewDecoder(strings.NewReader(in), WithCharsetReader(func(label string, r io.Reader) (io.Reader, error) { return r, nil }))
	for {
		_, err := d.ReadToken()
		l, _ := d.InputPos()
		lines = append(lines, l)
		if err != nil {
			break
		}
	}
	if got := lines[len(lines)-1]; got != 5 {
		t.Fatalf("lines = %v, want to end at 5", lines)
	}
}

// BenchmarkInputPosEveryToken must scale linearly with the input size.
func BenchmarkInputPosEveryToken(b *testing.B) {
	for _, items := range []int{25000, 100000, 400000} {
		data := []byte("<r>\n" + strings.Repeat("<item id=\"x\">hello &amp; world</item>\n", items) + "</r>")
		b.Run(fmt.Sprintf("%dKB", len(data)>>10), func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for range b.N {
				d := NewDecoderBytes(data)
				for {
					if _, err := d.ReadToken(); err != nil {
						break
					}
					d.InputPos()
				}
			}
		})
	}
}

func TestInputPosLinear(t *testing.T) {
	run := func(items int) time.Duration {
		data := []byte("<r>\n" + strings.Repeat("<item id=\"x\">hello &amp; world</item>\n", items) + "</r>")
		start := time.Now()
		d := NewDecoderBytes(data)
		for {
			if _, err := d.ReadToken(); err != nil {
				break
			}
			d.InputPos()
		}
		return time.Since(start)
	}
	// 1.5 MB took 0.7 s per 0.95 MB when quadratic; linear stays within tens of ms.
	if el := run(40000); el > time.Second {
		t.Fatalf("InputPos after every token took %v for 1.6 MB", el)
	}
}
