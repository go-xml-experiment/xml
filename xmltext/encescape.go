// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import "unicode/utf8"

// Escape modes.
const (
	escText      = iota // v2 character data: & < \r, and > only as the end of "]]>"
	escAttr             // v2 attribute value: & < " \t \n \r
	escLegText          // encoding/xml EncodeToken(CharData): " ' & < > \t \r (newline kept)
	escLegTextNL        // encoding/xml escaped string content: as escLegText plus \n
	escLegAttr          // encoding/xml attribute value: same as escLegTextNL
	escModes
)

// Escape classes in the per-mode tables.
const (
	ecPlain = iota
	ecAmp
	ecLT
	ecGT     // ">" escaped unconditionally
	ecGTCond // ">" escaped only after "]]"
	ecQuot   // v2 &quot;
	ecQuot34 // legacy &#34;
	ecApos39 // legacy &#39;
	ecTab
	ecNL
	ecCR
	ecCtl  // control character XML forbids
	ecHigh // >= 0x80: validate UTF-8 and the XML Char range
)

var escTable = func() (t [escModes][256]uint8) {
	for m := range t {
		for c := 0; c < 0x20; c++ {
			t[m][c] = ecCtl
		}
		for c := 0x80; c < 256; c++ {
			t[m][c] = ecHigh
		}
		t[m]['&'], t[m]['<'] = ecAmp, ecLT
		t[m]['\r'] = ecCR
		t[m]['\t'], t[m]['\n'] = ecPlain, ecPlain
	}
	t[escText]['>'] = ecGTCond
	t[escAttr]['"'], t[escAttr]['\t'], t[escAttr]['\n'] = ecQuot, ecTab, ecNL
	for _, m := range []int{escLegText, escLegTextNL, escLegAttr} {
		t[m]['>'], t[m]['"'], t[m]['\''] = ecGT, ecQuot34, ecApos39
		t[m]['\t'] = ecTab
	}
	t[escLegTextNL]['\n'] = ecNL
	t[escLegAttr]['\n'] = ecNL
	return
}()

var escStrings = [...]string{
	ecAmp: "&amp;", ecLT: "&lt;", ecGT: "&gt;", ecGTCond: "&gt;", ecQuot: "&quot;",
	ecQuot34: "&#34;", ecApos39: "&#39;", ecTab: "&#x9;", ecNL: "&#xA;", ecCR: "&#xD;",
}

// escapeInto appends s to dst with the escapes of mode. carry is the number
// of ']' bytes (0..2) that directly precede s in the output, for "]]>".
// In the legacy modes invalid UTF-8 and forbidden characters become U+FFFD;
// otherwise they are an error.
func (e *Encoder) escapeInto(dst, s []byte, mode int, carry int) ([]byte, error) {
	tab := &escTable[mode]
	legacy := mode >= escLegText
	last := 0
	kernelAt := 0 // do not retry the vector kernel before this index (dense non-ASCII text)
	for i := 0; i < len(s); {
		if i >= kernelAt && len(s)-i >= 16 {
			if n := escPrefix(s[i:]); n > 0 {
				i += n
				if n < 16 {
					kernelAt = i + 8 // an escape is close by: dense escapes are cheaper bytewise
				}
				continue
			}
			if s[i] >= 0x80 {
				kernelAt = i + 16 // dense non-ASCII: do not pay the call per character
			}
		}
		c := s[i]
		k := tab[c]
		if k == ecPlain {
			i++
			continue
		}
		width := 1
		switch k {
		case ecHigh:
			if plain3(s[i:]) {
				i += 3
				continue
			}
			r, n := utf8.DecodeRune(s[i:])
			if r == utf8.RuneError && n == 1 || !validXMLRune(r) {
				if !legacy {
					return dst, errInvalidChar(s, i)
				}
				dst = append(dst, s[last:i]...)
				dst = append(dst, "�"...)
				i += n
				last = i
				continue
			}
			i += n
			continue
		case ecCtl:
			if !legacy {
				return dst, errInvalidChar(s, i)
			}
			dst = append(dst, s[last:i]...)
			dst = append(dst, "�"...)
			i++
			last = i
			continue
		case ecGTCond:
			if !bracketsBefore(s, i, carry) {
				i++
				continue
			}
		}
		dst = append(dst, s[last:i]...)
		dst = append(dst, escStrings[k]...)
		i += width
		last = i
	}
	return appendShort(dst, s[last:]), nil
}

// bracketsBefore reports whether the two bytes before s[i] are "]]", looking
// into the previous output (carry) when i < 2.
func bracketsBefore(s []byte, i, carry int) bool {
	switch {
	case i >= 2:
		return s[i-1] == ']' && s[i-2] == ']'
	case i == 1:
		return s[0] == ']' && carry >= 1
	}
	return carry >= 2
}

type charError struct {
	b   byte
	off int
}

func (c charError) Error() string {
	return "xmltext: invalid UTF-8 or character not allowed in XML at byte " + itoa(c.off)
}

func errInvalidChar(s []byte, i int) error { return charError{s[i], i} }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	p := len(b)
	for n > 0 {
		p--
		b[p] = byte('0' + n%10)
		n /= 10
	}
	return string(b[p:])
}
