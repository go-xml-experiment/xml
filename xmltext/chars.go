// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"unicode/utf8"

	"github.com/go-xml-experiment/xml/internal/xmlname"
)

// Byte classes for the text scanner.
const (
	cNormal  = 0
	cAmp     = 1 // '&'
	cCR      = 2 // '\r'
	cBad     = 3 // control character not allowed in XML
	cHigh    = 4 // >= 0x80, needs UTF-8 validation
	cBracket = 5 // ']' (for "]]>" detection)
	cSpace   = 6 // tab, newline, '\r' in an attribute value (attrClass only)
)

var textClass = func() (t [256]uint8) {
	for i := 0; i < 0x20; i++ {
		t[i] = cBad
	}
	t['\t'], t['\n'] = cNormal, cNormal
	t['\r'] = cCR
	t['&'] = cAmp
	t[']'] = cBracket
	for i := 0x80; i < 256; i++ {
		t[i] = cHigh
	}
	return
}()

// attrClass is textClass for attribute values of the default profile, where
// literal tab, newline and carriage return are normalized to a space
// (XML 1.0 §3.3.3) while references to them are kept.
var attrClass = func() (t [256]uint8) {
	t = textClass
	t['\t'], t['\n'], t['\r'] = cSpace, cSpace, cSpace
	return
}()

// nameByte classifies ASCII bytes for names: 1 = name start, 2 = name char only.
var nameClass = func() (t [256]uint8) {
	for c := 'a'; c <= 'z'; c++ {
		t[c] = 1
	}
	for c := 'A'; c <= 'Z'; c++ {
		t[c] = 1
	}
	t['_'], t[':'] = 1, 1
	for c := '0'; c <= '9'; c++ {
		t[c] = 2
	}
	t['-'], t['.'] = 2, 2
	for i := 0x80; i < 256; i++ {
		t[i] = 3 // non-ASCII: decode
	}
	return
}()

func isNameStartRune(r rune) bool {
	switch {
	case r == ':' || r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z':
		return true
	case r >= 0xC0 && r <= 0xD6, r >= 0xD8 && r <= 0xF6, r >= 0xF8 && r <= 0x2FF,
		r >= 0x370 && r <= 0x37D, r >= 0x37F && r <= 0x1FFF, r >= 0x200C && r <= 0x200D,
		r >= 0x2070 && r <= 0x218F, r >= 0x2C00 && r <= 0x2FEF, r >= 0x3001 && r <= 0xD7FF,
		r >= 0xF900 && r <= 0xFDCF, r >= 0xFDF0 && r <= 0xFFFD, r >= 0x10000 && r <= 0xEFFFF:
		return true
	}
	return false
}

func isNameRune(r rune) bool {
	if isNameStartRune(r) {
		return true
	}
	return r == '-' || r == '.' || r >= '0' && r <= '9' || r == 0xB7 ||
		r >= 0x300 && r <= 0x36F || r >= 0x203F && r <= 0x2040
}

// validName selects the input profile's name rules without changing encoding.
func (d *Decoder) validName(b []byte) bool {
	if d.perm {
		return xmlname.Valid(b)
	}
	return validName(b)
}

// validName reports whether b is a well-formed XML Name.
func validName(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	for i := 0; i < len(b); {
		c := b[i]
		switch cl := nameClass[c]; {
		case cl == 3:
			r, n := utf8.DecodeRune(b[i:])
			if r == utf8.RuneError && n <= 1 {
				return false
			}
			if i == 0 && !isNameStartRune(r) || !isNameRune(r) {
				return false
			}
			i += n
			continue
		case cl == 0, cl == 2 && i == 0:
			return false
		}
		i++
	}
	return true
}

func isSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' }

// validXMLRune reports whether r is in the XML Char production.
func validXMLRune(r rune) bool {
	return r == 0x9 || r == 0xA || r == 0xD || r >= 0x20 && r <= 0xD7FF ||
		r >= 0xE000 && r <= 0xFFFD || r >= 0x10000 && r <= 0x10FFFF
}
