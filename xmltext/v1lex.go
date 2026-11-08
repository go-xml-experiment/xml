// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"strings"
)

// v1StartTagErr re-lexes the start tag b (from its '<', with absolute offset
// base) byte by byte the way encoding/xml's lexer does and returns the first
// error it would report, with its message and line number. It is used only on
// the error path of the permissive profiles, so that malformed input produces
// the same error text as encoding/xml. truncated means the input ended inside
// the tag. It returns nil if encoding/xml's lexer would not fail.
func (d *Decoder) v1StartTagErr(b []byte, base int, truncated bool) error {
	eof := func() error {
		if truncated {
			return d.syntaxErr(base+len(b), "unexpected EOF")
		}
		return nil
	}
	fail := func(msg string, off int) error { return d.syntaxErr(base+off, msg) }
	run := func(from int) int {
		j := from
		for j < len(b) && (isNameByte(b[j]) || b[j] >= 0x80) {
			j++
		}
		return j
	}
	// nsname reads a name starting at i; ok is false with err set (or nil for
	// "caller supplies the message").
	nsname := func(i int, what string) (end int, err error, ok bool) {
		j := run(i)
		if j == i {
			return i, fail(what, i), false
		}
		if j == len(b) {
			return j, eof(), false
		}
		if !d.validName(b[i:j]) {
			return j, fail("invalid XML name: "+string(b[i:j]), j), false
		}
		if bytes.Count(b[i:j], []byte{':'}) > 1 {
			return j, fail(what, j), false
		}
		return j, nil, true
	}
	skip := func(i int) int {
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		return i
	}
	j, err, ok := nsname(1, "expected element name after <")
	if !ok {
		return err
	}
	i := j
	for {
		i = skip(i)
		if i >= len(b) {
			return eof()
		}
		c := b[i]
		i++
		if c == '/' {
			if i >= len(b) {
				return eof()
			}
			if b[i] != '>' {
				return fail("expected /> in element", i+1)
			}
			return nil
		}
		if c == '>' {
			return nil
		}
		i--
		j, err, ok = nsname(i, "expected attribute name in element")
		if !ok {
			return err
		}
		i = skip(j)
		if i >= len(b) {
			return eof()
		}
		if b[i] != '=' {
			if d.legacy != nil && len(d.legacy.changes) != 0 {
				d.legacyAt(base + i)
			}
			if !d.loose {
				return fail("attribute name without = in element", i+1)
			}
			continue
		}
		i = skip(i + 1)
		if i >= len(b) {
			return eof()
		}
		q := b[i]
		if d.legacy != nil && len(d.legacy.changes) != 0 {
			d.legacyAt(base + i)
		}
		if q == '"' || q == '\'' {
			k := bytes.IndexByte(b[i+1:], q)
			if k < 0 {
				// no closing quote: errors in the part that is there still come first
				if err := d.checkAttrValue(b[i+1:], base+i+1); err != nil && truncated {
					if se, ok := err.(*SyntaxError); ok && strings.HasPrefix(se.Msg, "invalid character entity") {
						// se.Offset is where the lexer stopped, past the '&'.
						val := b[i+1:]
						p := bytes.LastIndexByte(val[:min(int(se.Offset)-base-(i+1), len(val))], '&')
						if p >= 0 && refRunReachesEnd(val, p) {
							return eof()
						}
					}
					return err
				}
				return eof()
			}
			if err := d.checkAttrValue(b[i+1:i+1+k], base+i+1); err != nil {
				return err
			}
			i += k + 2
			continue
		}
		if !d.loose {
			return fail("unquoted or missing attribute value in element", i+1)
		}
		for i < len(b) && unquotedAttr[b[i]] {
			i++
		}
		if i >= len(b) {
			return eof()
		}
	}
}

// checkAttrValue validates a quoted attribute value as the lexer reads it.
func (d *Decoder) checkAttrValue(val []byte, at int) error {
	if k := bytes.IndexByte(val, '<'); k >= 0 {
		return d.syntaxErr(at+k+1, "unescaped < inside quoted string")
	}
	first, err := d.scanText(val, at, false, true)
	if err != nil {
		return err
	}
	if first >= 0 {
		_, err = d.decodeFused(append(make([]byte, 0, len(val)), val[:first]...), val, first, at, false, true)
	}
	return err
}

// refRunReachesEnd reports whether the reference that starts at s[p] ('&') is
// cut off by the end of s, so encoding/xml would hit the end of input before
// it could judge it.
func refRunReachesEnd(s []byte, p int) bool {
	j := p + 1
	if j < len(s) && s[j] == '#' {
		j++
		hex := false
		if j < len(s) && s[j] == 'x' {
			j++
			hex = true
		}
		for j < len(s) && ('0' <= s[j] && s[j] <= '9' || hex && ('a' <= s[j] && s[j] <= 'f' || 'A' <= s[j] && s[j] <= 'F')) {
			j++
		}
	} else {
		for j < len(s) && (isNameByte(s[j]) || s[j] >= 0x80) {
			j++
		}
	}
	return j >= len(s)
}
