// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"strings"
)

// Range parsers: each takes a complete token located by the boundary engine
// and fills the decoder's current-token fields. They never search for
// delimiters beyond the token's own interior structure.

// scratchFor returns the empty decode scratch with room for n bytes. A
// reference never decodes to more bytes than it takes in the source (custom
// entities can, and append then grows the slice), so n is the raw length.
// The old contents are dead, so a larger buffer is made rather than grown:
// make leaves the pages of fresh memory untouched, and nothing is copied.
func (d *Decoder) scratchFor(n int) []byte {
	if cap(d.scratch) < n {
		return make([]byte, 0, n)
	}
	return d.scratch[:0]
}

func (d *Decoder) parseText(bd boundary) error {
	if bd.flags&flagChecked != 0 {
		d.pendChar = nil
		return d.finishText(bd, int(bd.first))
	}
	start, end := bd.start, bd.end
	body := d.buf[start:end]
	first, err := d.scanText(body, start, true, true)
	if err != nil {
		return err
	}
	return d.finishText(bd, first)
}

// finishText completes parseText for text that has passed scanText, whose
// result is first.
func (d *Decoder) finishText(bd boundary, first int) error {
	start, end := bd.start, bd.end
	body := d.buf[start:end]
	if d.rootDepthCheck(body) {
		return d.syntaxErr(start, "text outside of root element")
	}
	d.kind = Text
	d.partial = bd.flags&flagPartial != 0
	d.tokS, d.tokE = start, end
	d.verbatim = first < 0
	if first >= 0 {
		var err error
		d.scratch = d.scratchFor(len(body))
		d.scratch, err = d.decodeFused(append(d.scratch, body[:first]...), body, first, start, true, true)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Decoder) parseComment(bd boundary) error {
	body := d.buf[bd.start+4 : bd.end-3]
	if d.perm {
		// encoding/xml notices "--" when it reads the byte after it
		if off := commentViolation(d.buf[bd.start+4 : bd.end]); off >= 0 {
			return d.syntaxErr(bd.start+4+off, `invalid sequence "--" not allowed in comments`)
		}
	} else {
		if i := bytes.Index(body, []byte("--")); i >= 0 {
			return d.syntaxErr(bd.start+4+i, `invalid sequence "--" not allowed in comments`)
		}
		if len(body) > 0 && body[len(body)-1] == '-' {
			return d.syntaxErr(bd.end-4, `invalid sequence "--" not allowed in comments`)
		}
	}
	if !d.perm {
		if _, _, err := d.classifyText(body, bd.start+4, false); err != nil {
			return err
		}
	}
	d.kind = Comment
	d.tokS, d.tokE = bd.start+4, bd.end-3
	d.verbatim = true
	return nil
}

func (d *Decoder) parseCData(bd boundary) error {
	if len(d.stack) == 0 && !d.frags {
		return d.syntaxErr(bd.start, "CDATA outside of root element")
	}
	// A piece of a split section has no opening delimiter after the first and
	// no "]]>" before the last.
	s, e := bd.start, bd.end
	if bd.flags&flagCDHead != 0 {
		s += 9
	}
	d.partial = bd.flags&flagPartial != 0
	if !d.partial {
		e -= 3
	}
	body := d.buf[s:e]
	first, err := d.scanText(body, s, false, false)
	if err != nil {
		return err
	}
	d.kind = CData
	d.tokS, d.tokE = s, e
	d.verbatim = first < 0
	if first >= 0 {
		d.scratch = d.scratchFor(len(body))
		d.scratch, err = d.decodeFused(append(d.scratch, body[:first]...), body, first, s, false, false)
		if err != nil {
			return err
		}
	}
	return nil
}

func (d *Decoder) parseProcInst(bd boundary) error {
	body := d.buf[bd.start+2 : bd.end-2]
	n := 0
	if d.perm {
		// encoding/xml: the target ends at the first byte that cannot be in a name.
		for n < len(body) && nameClass[body[n]] != 0 {
			n++
		}
	} else {
		for n < len(body) && !isSpace(body[n]) {
			n++
		}
	}
	target := body[:n]
	if !d.validName(target) {
		if d.perm && n > 0 {
			return d.syntaxErr(bd.start+2+n, "invalid XML name: "+string(target))
		}
		return d.syntaxErr(bd.start+2, "expected target name after <?")
	}
	ds := n
	for ds < len(body) && isSpace(body[ds]) {
		ds++
	}
	data := body[ds:]
	if !d.perm {
		if _, _, err := d.classifyText(data, bd.start+2+ds, false); err != nil {
			return err
		}
	}
	if d.perm {
		if string(target) == "xml" {
			if d.opts.LegacyDecoder != nil {
				// The legacy lexer has consumed the entire PI before checking it.
				d.pos = bd.end
				d.opts.CharsetReader = *d.opts.LegacyDecoder.CharsetReader
			}
			if err := d.checkXMLDecl(data, bd.start+2+ds); err != nil {
				return err
			}
		}
	} else {
		// Namespaces in XML: no colons in targets. XML 1.0 §2.6: [Xx][Mm][Ll] is
		// reserved, and the XML declaration is the first thing in the document.
		if bytes.IndexByte(target, ':') >= 0 {
			return d.syntaxErr(bd.start+2, "processing instruction target "+string(target)+" contains a colon")
		}
		if isXMLTarget(target) {
			if string(target) != "xml" {
				return d.syntaxErr(bd.start+2, "processing instruction target "+string(target)+" is reserved")
			}
			if d.base+int64(bd.start) != d.docStart {
				return d.syntaxErr(bd.start+2, "XML declaration is only allowed at the start of the document")
			}
			if err := d.checkXMLDecl(data, bd.start+2+ds); err != nil {
				return err
			}
		}
	}
	d.kind = ProcInst
	d.nameS, d.nameE, d.colon = bd.start+2, bd.start+2+n, -1
	d.tokS, d.tokE = bd.start+2+ds, bd.end-2
	d.verbatim = true
	return nil
}

func (d *Decoder) checkXMLDecl(data []byte, at int) error {
	var version, enc string
	if d.perm {
		version, enc = legacyProcInst(data, "version"), legacyProcInst(data, "encoding")
	} else {
		var off int
		var msg string
		if version, enc, off, msg = parseXMLDecl(data); msg != "" {
			// a wrong version is reported as such even when other parts are malformed
			if version != "" && version != "1.0" {
				return d.syntaxErr(at, "unsupported version: "+version+"; only version 1.0 is supported")
			}
			return d.syntaxErr(at+off, msg)
		}
	}
	if version != "1.0" && (version != "" || !d.perm) {
		return d.syntaxErr(at, "unsupported version: "+version+"; only version 1.0 is supported")
	}
	if enc != "" && !strings.EqualFold(enc, "utf-8") {
		if d.opts.CharsetReader == nil {
			return d.syntaxErr(at, "unsupported encoding: "+enc)
		}
		var err error
		d.charsetPending, err = d.openCharset(enc, at+len(data)+2)
		d.rare = d.rare || d.charsetPending != nil
		return err
	}
	return nil
}

// isXMLTarget reports whether a processing instruction target is "xml" in any
// letter case, the names XML 1.0 reserves.
func isXMLTarget(t []byte) bool {
	return len(t) == 3 && t[0]|0x20 == 'x' && t[1]|0x20 == 'm' && t[2]|0x20 == 'l'
}

// parseXMLDecl checks the pseudo-attributes of an XML declaration (the text
// after "<?xml" and its whitespace) against XMLDecl: version, then an optional
// encoding, then an optional standalone, separated by whitespace. It returns
// the version and encoding values, or an error message with its offset in data.
// version is also returned alongside an error once it has been read.
func parseXMLDecl(data []byte) (version, enc string, off int, msg string) {
	i := 0
	skip := func() int {
		n := i
		for i < len(data) && isSpace(data[i]) {
			i++
		}
		return i - n
	}
	// pseudo reads name Eq quoted-value at i and returns the value.
	pseudo := func(name string) (string, bool) {
		i += len(name)
		skip()
		if i >= len(data) || data[i] != '=' {
			msg, off = "expected = after "+name+" in XML declaration", i
			return "", false
		}
		i++
		skip()
		if i >= len(data) || data[i] != '"' && data[i] != '\'' {
			msg, off = "unquoted value of "+name+" in XML declaration", i
			return "", false
		}
		q := data[i]
		j := bytes.IndexByte(data[i+1:], q)
		if j < 0 {
			msg, off = "unterminated value of "+name+" in XML declaration", i
			return "", false
		}
		v := string(data[i+1 : i+1+j])
		i += j + 2
		return v, true
	}
	has := func(name string) bool {
		return len(data)-i >= len(name) && string(data[i:i+len(name)]) == name
	}
	if !has("version") {
		return "", "", 0, "XML declaration must begin with version"
	}
	var ok bool
	if version, ok = pseudo("version"); !ok {
		return "", "", off, msg
	}
	stage := 1 // pseudo-attributes seen so far: 1 version, 2 encoding, 3 standalone
	for {
		if skip() == 0 && i < len(data) {
			return version, enc, i, "expected whitespace in XML declaration"
		}
		if i >= len(data) {
			return version, enc, 0, ""
		}
		switch {
		case stage < 2 && has("encoding"):
			at := i
			if enc, ok = pseudo("encoding"); !ok {
				return version, "", off, msg
			}
			if !validEncName(enc) {
				return version, "", at, "invalid encoding name " + enc + " in XML declaration"
			}
			stage = 2
		case stage < 3 && has("standalone"):
			at := i
			v, ok := pseudo("standalone")
			if !ok {
				return version, enc, off, msg
			}
			if v != "yes" && v != "no" {
				return version, enc, at, "standalone must be yes or no in XML declaration"
			}
			stage = 3
		default:
			return version, enc, i, "unexpected text in XML declaration (expected encoding, standalone or end)"
		}
	}
}

// validEncName reports whether s matches EncName: [A-Za-z] ([A-Za-z0-9._] | '-')*.
func validEncName(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
		case i > 0 && (c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-'):
		default:
			return false
		}
	}
	return s != ""
}

// legacyProcInst is encoding/xml's matching of a pseudo-attribute: the text
// "param=" immediately followed by a quote, anywhere in the declaration.
func legacyProcInst(data []byte, param string) string {
	s := string(data)
	param += "="
	lenp := len(param)
	i := 0
	var sep byte
	for i < len(s) {
		sub := s[i:]
		k := strings.Index(sub, param)
		if k < 0 || lenp+k >= len(sub) {
			return ""
		}
		i += lenp + k + 1
		if c := sub[lenp+k]; c == '\'' || c == '"' {
			sep = c
			break
		}
	}
	if sep == 0 {
		return ""
	}
	j := strings.IndexByte(s[i:], sep)
	if j < 0 {
		return ""
	}
	return s[i : i+j]
}

func (d *Decoder) parseDirective(bd boundary) error {
	body := d.buf[bd.start+2 : bd.end-1]
	d.kind = Directive
	d.tokS, d.tokE = bd.start+2, bd.end-1
	d.verbatim = true
	if d.perm {
		if bytes.Contains(body, commentOpen) {
			d.scratch = stripDirectiveComments(d.scratch[:0], body)
			d.verbatim = false
		}
		return nil
	}
	if len(body) == 0 || nameClass[body[0]] != 1 {
		return d.syntaxErr(bd.start, "invalid markup declaration")
	}
	if _, _, err := d.classifyText(body, bd.start+2, false); err != nil {
		return err
	}
	return nil
}

func (d *Decoder) parseEndTag(bd boundary) error {
	if d.raw {
		return d.parseEndTagRaw(bd)
	}
	name := d.buf[bd.start+2 : bd.end-1]
	for len(name) > 0 && isSpace(name[len(name)-1]) {
		name = name[:len(name)-1]
	}
	if len(d.stack) == 0 {
		return d.syntaxErr(bd.start, "unexpected end element </"+string(name)+">")
	}
	top := &d.stack[len(d.stack)-1]
	open := d.nameArena[top.nameOff : top.nameOff+top.nameLen]
	if !bytes.Equal(name, open) {
		if !d.validName(name) {
			return d.syntaxErr(bd.start+2, "expected element name after </")
		}
		if d.loose {
			colon, err := d.splitName(name, bd.start+2, "expected element name after </")
			if err != nil {
				return err
			}
			if !bytes.Equal(name[colon+1:], open[top.colon+1:]) {
				d.kind, d.popEnd, d.rawEnd = EndElement, true, true
				d.attrs = d.attrs[:0]
				d.bi-- // retry the same lexical end tag after popping this element
				if top.nsChanged {
					d.rare = true
					d.deferredErr = d.syntaxErr(bd.start, "unexpected end element </"+string(name)+">")
				}
				return nil
			}
		}
		if d.perm {
			return d.syntaxErr(bd.start, mismatchMsg(open, name))
		}
		return d.syntaxErr(bd.start, "element <"+string(open)+"> closed by </"+string(name)+">")
	}
	d.kind = EndElement
	d.popEnd = true
	d.attrs = d.attrs[:0]
	return nil
}

func (d *Decoder) shouldAutoClose(bd boundary) bool {
	if len(d.stack) == 0 {
		return false
	}
	top := d.stack[len(d.stack)-1]
	local := d.nameArena[top.nameOff+top.colon+1 : top.nameOff+top.nameLen]
	for _, name := range d.opts.AutoClose {
		if !strings.EqualFold(name, string(local)) {
			continue
		}
		if bd.kind == EndElement {
			end := bytes.TrimSpace(d.buf[bd.start+2 : bd.end-1])
			if colon := bytes.IndexByte(end, ':'); colon > 0 && colon < len(end)-1 {
				end = end[colon+1:]
			}
			return !strings.EqualFold(string(end), string(local))
		}
		return true
	}
	return false
}

var commentOpen = []byte("<!--")

// stripDirectiveComments appends body to dst with every "<!-- ... -->" outside
// quotes replaced by one space, as encoding/xml does, so that markup on both
// sides of a comment cannot join into something new.
func stripDirectiveComments(dst, body []byte) []byte {
	if len(body) == 0 {
		return dst
	}
	dst = append(dst, body[0]) // v1 does not interpret the first directive byte
	var quote byte
	for i := 1; i < len(body); {
		c := body[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '<' && bytes.HasPrefix(body[i:], commentOpen):
			j := bytes.Index(body[i+4:], dashDashGT)
			if j >= 0 {
				dst = append(dst, ' ')
				i += 4 + j + 3
				continue
			}
		}
		dst = append(dst, c)
		i++
	}
	return dst
}

// parseEndTagRaw reads an end tag without looking at the open elements, with
// the error wording of encoding/xml.
func (d *Decoder) parseEndTagRaw(bd boundary) error {
	body := d.buf[bd.start+2 : bd.end-1]
	n := 0
	for n < len(body) && (isNameByte(body[n]) || body[n] >= 0x80) {
		n++
	}
	name := body[:n]
	switch {
	case n == 0:
		return d.syntaxErr(bd.start+2, "expected element name after </")
	case !d.validName(name):
		return d.syntaxErr(bd.start+2, "invalid XML name: "+string(name))
	}
	colon, err := d.splitName(name, bd.start+2, "expected element name after </")
	if err != nil {
		return err
	}
	k := n
	for k < len(body) && isSpace(body[k]) {
		k++
	}
	if k < len(body) {
		_, local := rawSplit(name)
		return d.syntaxErr(bd.start+2+k+1, "invalid characters between </"+local+" and >")
	}
	d.kind = EndElement
	d.nameS, d.nameE, d.colon = bd.start+2, bd.start+2+n, colon
	d.attrs = d.attrs[:0]
	return nil
}

// commentViolation returns, for the bytes s that follow "<!--" up to the end of
// the comment, the offset just past the first byte that follows "--" without
// being '>' (where encoding/xml reports the error), or -1.
func commentViolation(s []byte) int {
	for i := 2; i < len(s); i++ {
		if s[i-2] == '-' && s[i-1] == '-' && s[i] != '>' {
			return i + 1
		}
	}
	return -1
}
