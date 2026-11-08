// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math/bits"
	"math/rand/v2"
	"strings"
	"testing"
)

// refScanText is the check scanText did before the text scanner was fused:
// the run is located with bytes.IndexByte, then checked up to its first '&'
// or '\r'. It reports the end of the run (-1 if b holds no '<'), that first
// byte (-1 if none) and whether the checked bytes are free of problems.
func refScanText(b []byte) (end, first int, ok bool) {
	end = bytes.IndexByte(b, '<')
	body := b
	if end >= 0 {
		body = b[:end]
	}
	for i := 0; i < len(body); i++ {
		switch textClass[body[i]] {
		case cAmp, cCR:
			return end, i, true
		case cBad:
			return end, -1, false
		case cHigh:
			n := seqLen(body[i:])
			if n == 0 {
				return end, -1, false
			}
			i += n - 1
		case cBracket:
			if i+2 < len(body) && body[i+1] == ']' && body[i+2] == '>' {
				return end, -1, false
			}
		}
	}
	return end, -1, true
}

// plainTextWord is the word test of textRun.
func plainTextWord(w uint64) bool {
	z := w ^ (ones * '<')
	return plainWord(w) && (z-ones)&^z&0x8080808080808080 == 0
}

// TestTextStops checks that the lowest flagged byte of textStops is the
// first byte scanTextRun must look at, on random words and on words made of
// the bytes near the borrow edges of the zero-byte tests.
func TestTextStops(t *testing.T) {
	edge := []byte{0, 1, 0x1f, 0x20, 0x21, '&', '\'', '%', ']', '\\', '^', '<', '=', ';', 'a', 0x7f, 0x80, 0xc3, 0xff}
	r := rand.New(rand.NewPCG(3, 4))
	var w [8]byte
	for n := range 2000000 {
		for k := range w {
			if n%2 == 0 {
				w[k] = byte(r.Uint32())
			} else {
				w[k] = edge[r.IntN(len(edge))]
			}
		}
		want := 8
		for k, c := range w {
			if scanClass[c] != cNormal || c < 0x20 { // tab and newline too
				want = k
				break
			}
		}
		m := textStops(binary.LittleEndian.Uint64(w[:]))
		got := 8
		if m != 0 {
			got = bits.TrailingZeros64(m) >> 3
		}
		if got != want {
			t.Fatalf("textStops(%q): first stop %d, want %d", w[:], got, want)
		}
	}
}

// refTextRun is textRun, the fused scan of the direct path before
// scanTextRun replaced it, kept to compare speed with.
func refTextRun(b []byte) (end, first int, ok bool) {
	cool := 0
	for i := 0; i < len(b); {
		stop := min(i+8, len(b))
		if i >= cool && i+8 <= len(b) {
			if plainTextWord(binary.LittleEndian.Uint64(b[i:])) {
				i += 8
				continue
			}
			cool = i + 24
			stop = min(i+32, len(b))
		}
		for ; i < stop; i++ {
			c := b[i]
			if c == '<' {
				return i, -1, true
			}
			switch textClass[c] {
			case cAmp, cCR:
				j := bytes.IndexByte(b[i+1:], '<')
				if j < 0 {
					return 0, 0, false
				}
				return i + 1 + j, i, true
			case cBad:
				return 0, 0, false
			case cHigh:
				if plain3(b[i:]) {
					i += 2
					continue
				}
				n := seqLen(b[i:])
				if n == 0 {
					return 0, 0, false
				}
				i += n - 1
			case cBracket:
				if i+2 < len(b) && b[i+1] == ']' && b[i+2] == '>' {
					return 0, 0, false
				}
			}
		}
	}
	return 0, 0, false
}

// scanAll runs scanTextRun over b, resuming at every offset in cuts as if
// the input had arrived in pieces ending there, and finally over all of b
// as the last piece of the input.
func scanAll(b []byte, cuts []int) (end, first, res int) {
	from, first := 0, -1
	for _, c := range cuts {
		if c < from || c > len(b) {
			continue
		}
		n, f, r := scanTextRun(b[:c], from, first)
		if r != textNeed {
			return n, f, r
		}
		from, first = n, f
	}
	return scanTextRun(b, from, first)
}

// checkScanAgainstRef compares scanTextRun, whole and resumed at cuts, with
// refScanText on b.
func checkScanAgainstRef(t *testing.T, b []byte, cuts []int) {
	t.Helper()
	wantEnd, wantFirst, wantOK := refScanText(b)
	for _, cs := range [][]int{nil, cuts} {
		end, first, res := scanAll(b, cs)
		switch res {
		case textBad:
			if wantOK {
				t.Fatalf("%q cuts %v: scanTextRun reports a problem at %d; reference finds none", b, cs, end)
			}
		case textDone:
			if !wantOK || end != wantEnd || first != wantFirst {
				t.Fatalf("%q cuts %v: scanTextRun = end %d first %d; reference end %d first %d ok %v", b, cs, end, first, wantEnd, wantFirst, wantOK)
			}
		case textNeed:
			if wantEnd >= 0 {
				t.Fatalf("%q cuts %v: scanTextRun needs input; reference ends at %d", b, cs, wantEnd)
			}
			// At the end of the input the run is checked if the scan reached
			// the end (t0Engine.text).
			checked := end == len(b)
			if checked && (!wantOK || first != wantFirst) {
				t.Fatalf("%q cuts %v: scanTextRun checked to the end, first %d; reference first %d ok %v", b, cs, first, wantFirst, wantOK)
			}
			if !checked && wantOK && wantFirst < 0 {
				// left unchecked: only allowed for an incomplete sequence or
				// ']' at the very end, which scanText then reports or accepts
				tail := b[end:]
				if len(tail) > 3 || len(tail) == 0 {
					t.Fatalf("%q cuts %v: scanTextRun stopped at %d of %d", b, cs, end, len(b))
				}
			}
		}
	}
}

var scanPieces = []string{
	"a", "plain text ", "The quick brown fox ", "가", "가나다", "😀", "𝄞", "é",
	"&amp;", "&lt;", "&#65;", "&#x1F600;", "&ent;", "&am", "&",
	"\r", "\r\n", "\n", "\t", "]", "]]", "]]>", "] ]>", "]>",
	"\x01", "\x0b", "\x7f", "\xff", "\xc0\x80", "\xed\xa0\x80", "\xef\xbf\xbe", "\xe4\xb8", "\xf0\x9f\x98",
	"<", "<b>", strings.Repeat("x", 70), strings.Repeat("가", 30),
}

func TestScanTextRunMatchesReference(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 20000 {
		var sb strings.Builder
		for n := r.IntN(12); n >= 0; n-- {
			sb.WriteString(scanPieces[r.IntN(len(scanPieces))])
		}
		b := []byte(sb.String())
		var cuts []int
		for c := 0; c < len(b); c += 1 + r.IntN(5) {
			cuts = append(cuts, c)
		}
		checkScanAgainstRef(t, b, cuts)
	}
}

func TestScanTextRunEveryCut(t *testing.T) {
	for _, s := range []string{
		"hello &amp; world<", "abc]]><", "abc]]<", "가나다라마바사<", "😀𝄞<", "a\r\nb<",
		strings.Repeat("plain ", 20) + "가<", strings.Repeat("x", 100) + "]]><",
		strings.Repeat("y", 63) + "\xe4\xb8\x80<", strings.Repeat("y", 63) + "\xe4\xb8<",
	} {
		b := []byte(s)
		for c := 0; c <= len(b); c++ {
			for c2 := c; c2 <= len(b); c2 += 7 {
				checkScanAgainstRef(t, b, []int{c, c2})
			}
		}
	}
}

func FuzzScanTextRun(f *testing.F) {
	for _, s := range scanPieces {
		f.Add([]byte(s+"text<"), uint8(3))
	}
	f.Fuzz(func(t *testing.T, b []byte, step uint8) {
		var cuts []int
		for c := 0; c < len(b); c += 1 + int(step%16) {
			cuts = append(cuts, c)
		}
		checkScanAgainstRef(t, b, cuts)
	})
}

func BenchmarkScanKernel(b *testing.B) {
	for _, shape := range []string{"plain", "korean", "mixed", "entFront"} {
		for _, n := range []int{16, 64, 256, 4 << 10, 32 << 10} {
			buf := []byte(scanBenchText(shape, n) + "</p>")
			b.Run(fmt.Sprintf("%s/%d/fused", shape, n), func(b *testing.B) {
				b.SetBytes(int64(n))
				for b.Loop() {
					scanTextRun(buf, 0, -1)
				}
			})
			b.Run(fmt.Sprintf("%s/%d/textRun", shape, n), func(b *testing.B) {
				b.SetBytes(int64(n))
				for b.Loop() {
					refTextRun(buf)
				}
			})
			b.Run(fmt.Sprintf("%s/%d/indexThenScanText", shape, n), func(b *testing.B) {
				b.SetBytes(int64(n))
				d := NewDecoderBytes(nil)
				for b.Loop() {
					end := bytes.IndexByte(buf, '<')
					d.scanText(buf[:end], 0, true, true)
				}
			})
			b.Run(fmt.Sprintf("%s/%d/indexThenCheck", shape, n), func(b *testing.B) {
				b.SetBytes(int64(n))
				for b.Loop() {
					end := bytes.IndexByte(buf, '<')
					checkText(buf[:end], 0, false)
				}
			})
		}
	}
}
