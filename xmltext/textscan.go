// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"unicode/utf8"
)

// The text scanner. Locating a text run and checking its characters are one
// operation, scanTextRun, shared by the direct path (readDirect) and the
// boundary engine (t0Engine.text). Its result tells the parser how much of
// the run is already checked, so parseText does not read those bytes again:
//
//	textDone  the run ends at '<'; the bytes before its first '&' or '\r'
//	          are valid character data
//	textNeed  b holds no '<'; the scan resumes at the offset it returns
//	textBad   the run has a byte that the checked path (scanText) reports
//
// The scanner never makes errors. Every problem goes back to scanText, which
// knows the profile's rules: where an error is positioned and whether it is
// deferred to the end of the run.
//
// The bytes from the first '&' or '\r' on are left to decodeFused, which
// checks them while it resolves references and line ends. Checking them here
// as well would read them twice.
const (
	textDone = iota
	textNeed
	textBad
)

// scanTextRun scans the text run at the start of b from offset from. first
// is the offset of the run's first '&' or '\r' if an earlier call found one,
// or -1. It returns
//
//   - textDone, the offset of the '<' that ends the run and first;
//   - textNeed, the offset at which a later call over a longer b resumes, and
//     first; the bytes before that offset are checked up to first;
//   - textBad and the offset of the problem.
//
// The bytes before from must have been scanned by an earlier call with the
// same start of b.
func scanTextRun(b []byte, from, first int) (int, int, int) {
	if first >= 0 {
		return findLT(b, from, first)
	}
	// One loop looks for '<' and checks the bytes at once, a word at a time
	// where the words are plain ASCII. Dense text (non-ASCII, references)
	// is checked byte by byte, and a separate search for '<' would only add
	// a pass. On plain ASCII two passes cost less: the vectorized search
	// for '<', then a word loop that does not look for it. So after
	// plainRun plain words in a row the loop hands over to the two passes.
	i := from
	cool, plain := 0, 0
	for i < len(b) {
		stop := min(i+8, len(b))
		if i >= cool && i+8 <= len(b) {
			if w := binary.LittleEndian.Uint64(b[i:]); w&hi8 == 0 {
				m := textStops(w)
				if m == 0 {
					i += 8
					if plain++; plain == plainRun {
						if i < len(b) && b[i] == '<' { // a run of plainRun words exactly
							return i, -1, textDone
						}
						return scanTextLong(b, i)
					}
					continue
				}
				// skip the plain bytes before the first byte that needs a look
				i += bits.TrailingZeros64(m) >> 3
				if b[i] == '<' {
					return i, -1, textDone
				}
			}
			// A word with non-ASCII bytes goes to the byte loop as it is: dense
			// text is cheaper to walk than to mask.
			plain = 0
			cool = i + 24
			stop = min(i+32, len(b))
		}
		for ; i < stop; i++ {
			// branches in order of frequency, not a switch: the order of the
			// compiled comparisons decides the speed on dense text
			cl := scanClass[b[i]]
			if cl == cNormal {
				continue
			}
			if cl == cHigh {
				if plain3(b[i:]) {
					i += 2
					continue
				}
				n := seqLen(b[i:])
				if n == 0 {
					if !utf8.FullRune(b[i:]) {
						return i, -1, textNeed // b ends inside the sequence
					}
					return i, -1, textBad
				}
				i += n - 1
				continue
			}
			switch cl {
			case cLT:
				return i, -1, textDone
			case cAmp, cCR:
				return findLT(b, i+1, i)
			case cBad:
				return i, -1, textBad
			case cBracket:
				if i+2 < len(b) {
					if b[i+1] == ']' && b[i+2] == '>' {
						return i, -1, textBad
					}
				} else if i+1 == len(b) || b[i+1] == ']' {
					return i, -1, textNeed // "]]>" may be cut by the end of b
				}
			}
		}
	}
	return len(b), -1, textNeed
}

// scanClass is textClass with a class of its own for '<', which ends a run.
var scanClass = func() [256]uint8 {
	t := textClass
	t['<'] = cLT
	return t
}()

const cLT = 7

// plainRun is how many plain ASCII words in a row make scanTextRun search
// for '<' before it checks the rest of the run.
const plainRun = 8

// scanTextLong is scanTextRun from b[i:] in two passes.
func scanTextLong(b []byte, i int) (int, int, int) {
	end := len(b)
	if j := bytes.IndexByte(b[i:], '<'); j >= 0 {
		end = i + j
	}
	f, next, res := checkText(b[:end], i, end == len(b))
	switch {
	case res != textDone:
		return next, -1, res
	case end < len(b):
		return end, f, textDone
	case f < 0:
		return len(b), -1, textNeed
	}
	return len(b), f, textNeed // the rest only has to be searched for '<'
}

// findLT finishes scanTextRun for a run whose first '&' or '\r' is at first:
// what follows is checked by decodeFused, so only '<' is searched for.
func findLT(b []byte, from, first int) (int, int, int) {
	if j := bytes.IndexByte(b[from:], '<'); j >= 0 {
		return from + j, first, textDone
	}
	return len(b), first, textNeed
}

// checkText checks b[i:] as character data up to its first '&' or '\r'. It
// returns textDone with the offset of that byte, or -1 if there is none;
// textBad with the offset of a problem ("]]>" included); or, when more is
// set and b ends inside a UTF-8 sequence or in "]" or "]]", textNeed with the
// offset at which a check of a longer b resumes. It does the checks of
// scanText, and its word loop is the one of scanText.
func checkText(b []byte, i int, more bool) (first, next, res int) {
	cool := 0
	for i < len(b) {
		end := min(i+8, len(b))
		if i >= cool && i+8 <= len(b) {
			if plainWord(binary.LittleEndian.Uint64(b[i:])) {
				i += 8
				if len(b)-i >= 16 {
					i += plainPrefix(b[i:])
				}
				continue
			}
			cool = i + 24
			end = min(i+32, len(b))
		}
		for ; i < end; i++ {
			cl := textClass[b[i]]
			if cl == cNormal {
				continue
			}
			if cl == cHigh {
				if plain3(b[i:]) {
					i += 2
					continue
				}
				n := seqLen(b[i:])
				if n == 0 {
					if more && !utf8.FullRune(b[i:]) {
						return -1, i, textNeed
					}
					return -1, i, textBad
				}
				i += n - 1
				continue
			}
			switch cl {
			case cAmp, cCR:
				return i, i, textDone
			case cBad:
				return -1, i, textBad
			case cBracket:
				if i+2 < len(b) {
					if b[i+1] == ']' && b[i+2] == '>' {
						return -1, i, textBad
					}
				} else if more && (i+1 == len(b) || b[i+1] == ']') {
					return -1, i, textNeed
				}
			}
		}
	}
	return -1, len(b), textDone
}

const hi8 = 0x8080808080808080

// textStops returns, for the 8 bytes of w in little-endian order, a mask
// with the high bit of each byte set that scanTextRun must look at: '<',
// '&', ']', a byte below 0x20 or a byte of 0x80 and above. Tab, newline and
// carriage return are below 0x20 and so flagged too. The lowest set bit is
// exact: a borrow in a zero-byte test can only flag a byte above a byte
// that is flagged correctly. So bits.TrailingZeros64(m)/8 is the index of
// the first such byte, which is all the scanner relies on.
//
// This is the operation a vector kernel would do on 16 to 64 bytes at once:
// compare against the stop bytes, reduce to a bit mask, take the first set
// bit.
func textStops(w uint64) uint64 {
	const hi = hi8
	lt := ^((w | hi) - 0x2020202020202020)
	x := w ^ (ones * '&')
	y := w ^ (ones * ']')
	z := w ^ (ones * '<')
	return (w | lt | (x-ones)&^x | (y-ones)&^y | (z-ones)&^z) & hi
}
