// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import "encoding/binary"

// scanText validates body up to the first character that forces decoding
// ('&' when amps is set, or '\r') and returns its index, or -1 if body is
// entirely valid and verbatim.
func (d *Decoder) scanText(body []byte, at int, bracket, amps bool) (int, error) {
	d.pendChar = nil
	cool := 0
	for i := 0; i < len(body); {
		end := min(i+8, len(body))
		if i >= cool && i+8 <= len(body) {
			// one word inline (dense non-ASCII text fails it, and a call would cost
			// more than it finds); the kernel takes over after a plain word
			if plainWord(binary.LittleEndian.Uint64(body[i:])) {
				i += 8
				if len(body)-i >= 16 {
					i += plainPrefix(body[i:])
				}
				continue
			}
			cool = i + 24
			end = min(i+32, len(body))
		}
		for ; i < end; i++ {
			switch textClass[body[i]] {
			case cNormal:
			case cAmp:
				if amps {
					return i, nil
				}
			case cCR:
				return i, nil
			case cBad:
				if err := d.charProblem(body, i, at, flagsOf(bracket, amps, true)); err != nil {
					return 0, err
				}
			case cHigh:
				if plain3(body[i:]) {
					i += 2
					continue
				}
				n := seqLen(body[i:])
				if n == 0 {
					if err := d.charProblem(body, i, at, flagsOf(bracket, amps, false)); err != nil {
						return 0, err
					}
					break
				}
				i += n - 1
			case cBracket:
				if bracket && i+2 < len(body) && body[i+1] == ']' && body[i+2] == '>' {
					return 0, d.syntaxErr(d.bracketErrAt(at, i), "unescaped ]]> not in CDATA section")
				}
			}
		}
	}
	if e := d.pendChar; e != nil { // nothing else to decode: report the bad character now
		d.pendChar = nil
		return 0, e
	}
	return -1, nil
}

// charProblem handles a forbidden control character (ctl) or invalid UTF-8
// at body[i]. The default profile returns the error; the permissive profiles
// remember the first one and return nil so the scan continues (see deferChar).
// Kept out of line so the scanning loops stay small.
//
//go:noinline
func (d *Decoder) charProblem(body []byte, i, at int, fl uint8) error {
	bracket, amps, ctl := fl&1 != 0, fl&2 != 0, fl&4 != 0
	var msg string
	if ctl {
		msg = "illegal character code " + hexRune(rune(body[i]))
	} else {
		msg = badRuneMsg(body[i:])
	}
	e := d.syntaxErr(d.charErrAt(at, i, len(body), bracket, amps), msg)
	if d.deferChar(e) {
		return nil
	}
	return e
}

// deferChar records a bad-character error to be reported after the rest of the
// run, as encoding/xml does (it validates characters once all of the text has
// been read, while reference and "]]>" errors are reported as they are met). It
// reports whether the error was deferred; only the permissive profiles defer.
func (d *Decoder) deferChar(e error) bool {
	if !d.perm {
		return false
	}
	if d.pendChar == nil {
		d.pendChar = e
	}
	return true
}

// decodeFused validates body[from:] and appends it to dst with references
// (if amps) and line endings resolved, in a single pass. dst already holds
// the decoded form of body[:from].
func (d *Decoder) decodeFused(dst, body []byte, from, at int, bracket, amps bool) ([]byte, error) {
	run, cool := from, 0
	cls := &textClass
	if !bracket && amps && !d.perm { // attribute value
		cls = &attrClass
	}
	for i := from; i < len(body); {
		switch cls[body[i]] {
		case cNormal:
			i++
		case cSpace:
			// the line end \r\n counts as one character
			dst = append(dst, body[run:i]...)
			dst = append(dst, ' ')
			if body[i] == '\r' && i+1 < len(body) && body[i+1] == '\n' {
				i++
			}
			i++
			run = i
			i, cool = skipPlain(body, i, cool)
		case cAmp:
			if !amps {
				i++
				continue
			}
			dst = append(dst, body[run:i]...)
			if i+4 <= len(body) {
				// predefined entities without the ';' search
				var c byte
				n := 0
				switch binary.LittleEndian.Uint32(body[i:]) {
				case fourcc("&lt;"):
					c, n = '<', 4
				case fourcc("&gt;"):
					c, n = '>', 4
				case fourcc("&amp"):
					if i+5 <= len(body) && body[i+4] == ';' {
						c, n = '&', 5
					}
				case fourcc("&quo"):
					if i+6 <= len(body) && body[i+4] == 't' && body[i+5] == ';' {
						c, n = '"', 6
					}
				case fourcc("&apo"):
					if i+6 <= len(body) && body[i+4] == 's' && body[i+5] == ';' {
						c, n = '\'', 6
					}
				}
				if n != 0 {
					dst, i = append(dst, c), i+n
					run = i
					i, cool = skipPlain(body, i, cool)
					continue
				}
			}
			j := i + 1
			if d.perm {
				if j < len(body) && body[j] == '#' {
					j++
					hex := j < len(body) && body[j] == 'x'
					if hex {
						j++
					}
					for j < len(body) && (body[j] >= '0' && body[j] <= '9' || hex && (body[j] >= 'a' && body[j] <= 'f' || body[j] >= 'A' && body[j] <= 'F')) {
						j++
					}
				} else {
					for j < len(body) && nameClass[body[j]] != 0 {
						j++
					}
				}
				if j == len(body) && d.eof && at+len(body) == len(d.buf) {
					return dst, d.syntaxErr(at+j, "unexpected EOF")
				}
			} else if j < len(body) && body[j] == '#' {
				// any number of digits (leading zeros); the scan ends at the first
				// other byte, so a run of references stays linear
				j++
				hex := j < len(body) && body[j] == 'x'
				if hex {
					j++
				}
				for j < len(body) && (body[j] >= '0' && body[j] <= '9' || hex && (body[j] >= 'a' && body[j] <= 'f' || body[j] >= 'A' && body[j] <= 'F')) {
					j++
				}
			} else {
				lim := min(len(body), i+d.refMax)
				for j < lim && body[j] != ';' {
					j++
				}
				if j == lim {
					j = len(body)
				}
			}
			if d.legacy != nil && len(d.legacy.changes) != 0 {
				d.legacyAt(at + j)
			}
			ok := j < len(body) && body[j] == ';'
			if ok {
				dst, ok = d.appendRef(dst, body[i+1:j], at+j)
			}
			if !ok {
				if d.illegalRef {
					d.illegalRef = false
					if d.perm {
						// encoding/xml substitutes the character and rejects it with the rest of the run
						e := d.syntaxErr(d.charErrAt(at, i, len(body), bracket, amps), "illegal character code "+hexRune(d.illegalRune))
						d.deferChar(e)
						i = j + 1
						run = i
						i, cool = skipPlain(body, i, cool)
						continue
					}
					return dst, d.syntaxErr(at+i, "illegal character code in reference "+string(clip(body[i:j+1])))
				}
				if !d.unknownOK {
					if d.perm {
						return dst, d.syntaxErr(at+i+v1EntityEnd(body[i:]), "invalid character entity "+v1EntityText(body[i:]))
					}
					return dst, d.syntaxErr(at+i, "invalid character entity "+string(clip(body[i:])))
				}
				dst = append(dst, '&')
				i++
			} else {
				i = j + 1
			}
			run = i
			i, cool = skipPlain(body, i, cool)
		case cCR:
			dst = append(dst, body[run:i]...)
			i++
			if i < len(body) && body[i] == '\n' {
				run = i // keep the \n, drop the \r
				i, cool = skipPlain(body, i, cool)
			} else {
				dst = append(dst, '\n')
				run = i
				i, cool = skipPlain(body, i, cool)
			}
		case cBad:
			if err := d.charProblem(body, i, at, flagsOf(bracket, amps, true)); err != nil {
				return dst, err
			}
			i++
		case cHigh:
			if plain3(body[i:]) {
				i += 3
				continue
			}
			n := seqLen(body[i:])
			if n == 0 {
				if err := d.charProblem(body, i, at, flagsOf(bracket, amps, false)); err != nil {
					return dst, err
				}
				i++
				continue
			}
			i += n
		case cBracket:
			if bracket && i+2 < len(body) && body[i+1] == ']' && body[i+2] == '>' {
				return dst, d.syntaxErr(d.bracketErrAt(at, i), "unescaped ]]> not in CDATA section")
			}
			i++
		}
	}
	if e := d.pendChar; e != nil {
		d.pendChar = nil
		return dst, e
	}
	return append(dst, body[run:]...), nil
}

// skipPlain returns the end of the plain ASCII words of b from i on (see
// plainWord). decodeFused calls it after each reference and line end, the
// only places where a long run of plain text can begin, so the byte loop
// itself pays nothing for it.
//
// When there is no word to skip, the references are dense and a test per
// reference costs more than it saves (a predefined entity takes a few
// nanoseconds), so the test pauses until cool, 256 bytes on.
func skipPlain(b []byte, i, cool int) (int, int) {
	if i < cool {
		return i, cool
	}
	return skipWords(b, i)
}

// skipWords is the out-of-line part of skipPlain.
func skipWords(b []byte, i int) (int, int) {
	cool := 0
	k := i
	for k+8 <= len(b) && plainWord(binary.LittleEndian.Uint64(b[k:])) {
		k += 8
	}
	if k == i {
		cool = i + 256
	}
	return k, cool
}

func fourcc(s string) uint32 {
	return uint32(s[0]) | uint32(s[1])<<8 | uint32(s[2])<<16 | uint32(s[3])<<24
}

// plain3 reports whether b starts with a 3-byte UTF-8 sequence of U+1000..U+CFFF
// or U+D000..U+D7FF (Hangul, CJK, Kana ...), the common case that needs only
// continuation-byte checks; seqLen handles every other sequence.
func plain3(b []byte) bool {
	if len(b) < 3 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 {
		return false
	}
	c := b[0]
	return c >= 0xE1 && c <= 0xEC || c == 0xED && b[1] < 0xA0
}

// v1EntityText renders a bad reference as encoding/xml does: the '&' and the
// name or number that follows, then ";" or " (no semicolon)". b starts at '&'.
func v1EntityText(b []byte) string {
	r := v1EntityRead(b)
	if len(r) > 0 && r[len(r)-1] == ';' {
		return string(r)
	}
	return string(r) + " (no semicolon)"
}

// v1EntityRead returns the bytes of the reference that encoding/xml consumes.
func v1EntityRead(b []byte) []byte {
	j := 1
	if j < len(b) && b[j] == '#' {
		j++
		hex := false
		if j < len(b) && b[j] == 'x' {
			j++
			hex = true
		}
		for j < len(b) && ('0' <= b[j] && b[j] <= '9' || hex && ('a' <= b[j] && b[j] <= 'f' || 'A' <= b[j] && b[j] <= 'F')) {
			j++
		}
	} else {
		for j < len(b) && (isNameByte(b[j]) || b[j] >= 0x80) {
			j++
		}
	}
	if j < len(b) && b[j] == ';' {
		return b[:j+1]
	}
	return b[:j]
}

// charErrAt is where the error for a bad character is positioned. encoding/xml
// validates text after reading all of it (up to the '<', the closing quote or
// the end of the CDATA section), so the permissive profiles report the end of
// the run, which decides the line number; the default profile reports the
// character itself.
func (d *Decoder) charErrAt(at, i, n int, bracket, amps bool) int {
	if !d.perm {
		return at + i
	}
	switch {
	case bracket: // character data
		return at + n
	case amps: // attribute value: the closing quote has been read as well
		return at + n + 1
	}
	return at + n + 3 // CDATA: through "]]>"
}

// bracketErrAt is where "]]>" in character data is reported: after its '>'.
func (d *Decoder) bracketErrAt(at, i int) int {
	if d.perm {
		return at + i + 3
	}
	return at + i
}

// v1EntityEnd is the length of the reference text encoding/xml has read when it
// gives up: the name or number, plus the ';' if there is one.
func v1EntityEnd(b []byte) int {
	return len(v1EntityRead(b))
}

// flagsOf packs the text mode flags and "control character" for charProblem.
func flagsOf(bracket, amps, ctl bool) uint8 {
	var f uint8
	if bracket {
		f |= 1
	}
	if amps {
		f |= 2
	}
	if ctl {
		f |= 4
	}
	return f
}
