// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"slices"
	"unicode/utf8"
)

// unquotedAttr is the set of bytes encoding/xml accepts in an unquoted
// attribute value when Strict is false.
var unquotedAttr = func() (t [256]bool) {
	for c := 'a'; c <= 'z'; c++ {
		t[c], t[c-32] = true, true
	}
	for c := '0'; c <= '9'; c++ {
		t[c] = true
	}
	t['_'], t[':'], t['-'] = true, true, true
	return
}()

var tagStop = func() (t [256]bool) {
	t['>'], t['"'], t['\''] = true, true, true
	return
}()

func (d *Decoder) parseStartTag(bd boundary) error {
	if bd.flags&flagBare != 0 {
		return d.pushBare(bd)
	}
	tag := d.buf[bd.start:bd.end]
	at := bd.start
	// element name
	i := 1
	hasColon := false
	// Plain ASCII names are scanned and validated in one table pass; anything
	// else (non-ASCII, bad first byte) takes the general path.
	nameOK := false
	if nameClass[tag[1]] == 1 {
		hasColon = tag[1] == ':'
		i = 2
		for i < len(tag) {
			cl := nameClass[tag[i]]
			if cl == 0 || cl == 3 {
				break
			}
			hasColon = hasColon || tag[i] == ':'
			i++
		}
		nameOK = i < len(tag) && (isSpace(tag[i]) || tag[i] == '/' || tag[i] == '>')
	}
	if !nameOK {
		i, hasColon = 1, false
		for i < len(tag) && !isSpace(tag[i]) && tag[i] != '/' && tag[i] != '>' {
			hasColon = hasColon || tag[i] == ':'
			i++
		}
		if !d.validName(tag[1:i]) {
			return d.syntaxErr(at+1, d.badStartName(tag))
		}
	}
	name := tag[1:i]
	colon := -1
	if hasColon {
		var err error
		if colon, err = d.splitName(name, at+1, "expected element name after <"); err != nil {
			return err
		}
		if colon >= 0 && !d.perm && string(name[:colon]) == "xmlns" {
			return d.syntaxErr(at+1, "element name uses the reserved prefix xmlns")
		}
	}
	selfClose := tag[len(tag)-2] == '/'
	limit := len(tag) - 1
	if selfClose {
		limit--
	}

	d.attrs = d.attrs[:0]
	d.attrBuf = d.attrBuf[:0]
	d.attrSetOn = false
	hasDecl := false
	for {
		sp := i
		for i < limit && isSpace(tag[i]) {
			i++
		}
		if i >= limit {
			if i > limit && selfClose {
				return d.syntaxErr(at+i, "unexpected end of tag")
			}
			break
		}
		if i == sp && !(d.perm && len(d.attrs) > 0) {
			return d.syntaxErr(at+i, "expected whitespace between attributes")
		}
		if len(d.attrs) >= d.maxAttrs {
			return d.syntaxErr(at+i, "too many attributes in element (MaxAttributes)")
		}
		ns := i
		// As for the element name, plain ASCII names are scanned and validated
		// in one table pass.
		nameOK, hasColon := false, false
		if nameClass[tag[i]] == 1 {
			hasColon = tag[i] == ':'
			i++
			for i < limit {
				cl := nameClass[tag[i]]
				if cl == 0 || cl == 3 {
					break
				}
				hasColon = hasColon || tag[i] == ':'
				i++
			}
			nameOK = i >= limit || isSpace(tag[i]) || tag[i] == '='
		}
		if !nameOK {
			i = ns
			for i < limit && !isSpace(tag[i]) && tag[i] != '=' {
				i++
			}
			if !d.validName(tag[ns:i]) {
				return d.syntaxErr(at+ns, d.badAttrName(tag[ns:limit]))
			}
			hasColon = true // let splitName look
		}
		an := tag[ns:i]
		acolon := -1
		var err error
		if hasColon {
			if acolon, err = d.splitName(an, at+ns, "expected attribute name in element"); err != nil {
				return err
			}
		}
		for i < limit && isSpace(tag[i]) {
			i++
		}
		var (
			vs, ve int
			first  = -1
			val    []byte
			quoted = true
			plain  bool // value already validated by the single-pass scan below
			pfx    int  // bytes of a non-plain value the single-pass scan has cleared
		)
		switch {
		case i >= limit || tag[i] != '=':
			if d.legacy != nil && len(d.legacy.changes) != 0 {
				d.legacyAt(at + i)
			}
			if !d.loose {
				return d.syntaxErr(at+i, "attribute name without = in element")
			}
			// HTML-style attribute without a value: the value is its local name.
			quoted = false
			vs, ve = ns+acolon+1, ns+len(an)
			val = tag[vs:ve]
		default:
			i++
			for i < limit && isSpace(tag[i]) {
				i++
			}
			if d.legacy != nil && len(d.legacy.changes) != 0 {
				d.legacyAt(at + i)
			}
			if i < limit && (tag[i] == '"' || tag[i] == '\'') {
				q := tag[i]
				vs = i + 1
				// Short plain ASCII values are located and validated in one pass;
				// anything else ('<', '&', control, non-ASCII) takes the general path.
				e := vs
				for e < limit {
					if c := tag[e]; c == q || c < 0x20 || c == '<' || c == '&' || c >= 0x80 {
						break
					}
					e++
				}
				if e < limit && tag[e] == q {
					plain = true
					ve = e
				} else {
					pfx = e - vs
					j := bytes.IndexByte(tag[vs:limit], q)
					if j < 0 {
						return d.syntaxErr(at+vs, "unterminated attribute value")
					}
					ve = vs + j
				}
				val = tag[vs:ve]
				i = ve + 1
			} else if d.loose {
				quoted = false
				vs = i
				for i < limit && unquotedAttr[tag[i]] {
					i++
				}
				ve = i
				val = tag[vs:ve]
			} else {
				return d.syntaxErr(at+i, "unquoted or missing attribute value in element")
			}
		}
		if quoted && !plain {
			if k := bytes.IndexByte(val, '<'); k >= 0 {
				return d.syntaxErr(at+vs+k, "unescaped < inside quoted string")
			}
			var err error
			first, err = d.scanText(val, at+vs, false, true)
			if err != nil {
				return err
			}
			if !d.perm {
				first = firstAttrSpace(val, pfx, first)
			}
		}
		if !d.perm {
			dup := false
			if len(d.attrs) >= attrSetMin {
				dup = d.duplicateAttrSet(an)
			} else {
				for k := range d.attrs {
					if bytes.Equal(d.buf[d.attrs[k].name:d.attrs[k].nameEnd], an) {
						dup = true
						break
					}
				}
			}
			if dup {
				return d.syntaxErr(at+ns, "duplicate attribute "+string(an))
			}
		}
		span := attrSpan{name: at + ns, colon: acolon, nameEnd: at + ns + len(an), val: at + vs, valEnd: at + ve, verbatim: first < 0}
		if acolon >= 0 {
			span.decl = string(an[:acolon]) == "xmlns"
		} else {
			span.decl = string(an) == "xmlns"
		}
		hasDecl = hasDecl || span.decl
		if !span.verbatim {
			// resolve now so errors surface here; store range in attrBuf
			start := len(d.attrBuf)
			d.attrBuf = slices.Grow(d.attrBuf, len(val))
			d.attrBuf, err = d.decodeFused(append(d.attrBuf, val[:first]...), val, first, at+vs, false, true)
			if err != nil {
				return err
			}
			span.val, span.valEnd = start, len(d.attrBuf)
		}
		d.attrs = append(d.attrs, span)
	}
	return d.commitStartTag(at, name, colon, selfClose, hasDecl)
}

// readStartTag reads the start tag at d.buf[pos:] for readDirect in one pass,
// without locating it first, when it is well formed, its attribute values
// are quoted and it has fewer than attrSetMin attributes. It fills d.attrs as
// parseStartTag would. It reports false for any other tag, and for one that
// does not end inside the buffer, and parseStartTag then reads the tag from a
// boundary; that includes every tag with a syntax error, so errors are
// reported as before.
func (d *Decoder) readStartTag(pos int) (bool, error) {
	b := d.buf[pos:] // b[0] == '<'
	name, colon, i := scanQName(b, 1)
	if name == 0 {
		name, colon, i = wideName(b, 1)
	}
	if name == 0 || i >= len(b) || colon >= 0 && string(b[1:1+colon]) == "xmlns" {
		return false, nil
	}
	d.attrs = d.attrs[:0]
	d.attrBuf = d.attrBuf[:0]
	d.attrSetOn = false
	hasDecl := false
	for {
		sp := i
		for i < len(b) && isSpace(b[i]) {
			i++
		}
		if i >= len(b) {
			return false, nil
		}
		switch b[i] {
		case '>':
			return d.endStartTag(pos, i+1, name, colon, false, hasDecl)
		case '/':
			if i+1 < len(b) && b[i+1] == '>' {
				return d.endStartTag(pos, i+2, name, colon, true, hasDecl)
			}
			return false, nil
		}
		if i == sp || len(d.attrs) >= d.maxAttrs || len(d.attrs) >= attrSetMin-1 {
			return false, nil
		}
		ns := i
		n, acolon, j := scanQName(b, ns)
		if n == 0 {
			n, acolon, j = wideName(b, ns)
		}
		if n == 0 {
			return false, nil
		}
		an := b[ns:j]
		for i = j; i < len(b) && isSpace(b[i]); i++ {
		}
		if i >= len(b) || b[i] != '=' {
			return false, nil
		}
		for i++; i < len(b) && isSpace(b[i]); i++ {
		}
		if i >= len(b) || b[i] != '"' && b[i] != '\'' {
			return false, nil
		}
		q := b[i]
		vs := i + 1
		ve := vs
		for ve < len(b) {
			if c := b[ve]; c == q || c < 0x20 || c == '<' || c == '&' || c >= 0x80 {
				break
			}
			ve++
		}
		val, valEnd, verbatim := pos+vs, pos+ve, true
		if ve >= len(b) || b[ve] != q {
			var ok bool
			if ve, val, valEnd, verbatim, ok = d.wideValue(pos, vs, ve, q); !ok {
				return false, nil
			}
		}
		i = ve + 1
		for k := range d.attrs {
			if bytes.Equal(d.buf[d.attrs[k].name:d.attrs[k].nameEnd], an) {
				return false, nil
			}
		}
		span := attrSpan{name: pos + ns, colon: acolon, nameEnd: pos + j, val: val, valEnd: valEnd, verbatim: verbatim}
		if acolon >= 0 {
			span.decl = string(an[:acolon]) == "xmlns"
		} else {
			span.decl = string(an) == "xmlns"
		}
		hasDecl = hasDecl || span.decl
		d.attrs = append(d.attrs, span)
	}
}

// wideValue reads, for readStartTag, a quoted attribute value starting at
// d.buf[pos+vs:] whose bytes from pos+pfxEnd on are not plain ASCII:
// references, literal white space or non-ASCII characters. It does the checks
// and decoding of parseStartTag. It returns the index of the closing quote q
// relative to pos and the value as attrSpan holds it, or false if the value
// does not end inside the buffer or has an error, which parseStartTag then
// reports.
func (d *Decoder) wideValue(pos, vs, pfxEnd int, q byte) (ve, val, valEnd int, verbatim, ok bool) {
	b := d.buf[pos:]
	k := bytes.IndexByte(b[pfxEnd:], q)
	if k < 0 {
		return 0, 0, 0, false, false
	}
	ve = pfxEnd + k
	v, pfx := b[vs:ve], pfxEnd-vs
	if bytes.IndexByte(v[pfx:], '<') >= 0 {
		return 0, 0, 0, false, false
	}
	// v[:pfx] is plain ASCII, checked by readStartTag
	first, err := d.scanText(v[pfx:], pos+vs+pfx, false, true)
	if err != nil {
		return 0, 0, 0, false, false
	}
	if first >= 0 {
		first += pfx
	}
	if first = firstAttrSpace(v, pfx, first); first < 0 {
		return ve, pos + vs, pos + ve, true, true
	}
	start := len(d.attrBuf)
	d.attrBuf = slices.Grow(d.attrBuf, len(v))
	if d.attrBuf, err = d.decodeFused(append(d.attrBuf, v[:first]...), v, first, pos+vs, false, true); err != nil {
		return 0, 0, 0, false, false
	}
	return ve, start, len(d.attrBuf), false, true
}

// endStartTag completes readStartTag for a tag of n bytes.
func (d *Decoder) endStartTag(pos, n, name, colon int, selfClose, hasDecl bool) (bool, error) {
	if !d.eng.fits(n) {
		return false, nil
	}
	d.scanPos = pos + n
	if len(d.attrs) == 0 && colon < 0 && n-2-name == boolInt(selfClose) {
		return true, d.pushBare(boundary{start: pos, end: pos + n, kind: StartElement})
	}
	return true, d.commitStartTag(pos, d.buf[pos+1:pos+1+name], colon, selfClose, hasDecl)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// scanQName scans the ASCII name at b[i:] that readStartTag accepts. It
// returns the name's length, the offset of its colon or -1, and the index
// after it; a length of 0 means there is no such name at b[i:]: it does not
// start with a name start character, has a second colon, an empty prefix or
// local part, or a byte the table does not settle. wideName then decides.
func scanQName(b []byte, i int) (n, colon, end int) {
	start := i
	if i >= len(b) || nameClass[b[i]] != 1 || b[i] == ':' {
		return 0, -1, i
	}
	colon = -1
	for i++; i < len(b); i++ {
		c := b[i]
		if cl := nameClass[c]; cl == 0 || cl == 3 {
			if cl == 3 {
				return 0, -1, i
			}
			break
		}
		if c == ':' {
			if colon >= 0 {
				return 0, -1, i
			}
			colon = i - start
		}
	}
	n = i - start
	if colon >= 0 && (colon == n-1 || nameClass[b[start+colon+1]] != 1) {
		return 0, -1, i
	}
	return n, colon, i
}

// wideName decides a name that scanQName does not accept, such as one with
// a non-ASCII byte, with the checks of validName and splitName: a valid XML
// name with at most one colon, which neither starts nor ends it and is
// followed by a name start character. It returns what scanQName returns.
func wideName(b []byte, i int) (n, colon, end int) {
	end = i
	for end < len(b) && !isSpace(b[end]) && b[end] != '/' && b[end] != '>' && b[end] != '=' {
		end++
	}
	name := b[i:end]
	if !validName(name) {
		return 0, -1, end
	}
	colon = bytes.IndexByte(name, ':')
	if colon >= 0 && (colon == 0 || colon == len(name)-1 ||
		bytes.IndexByte(name[colon+1:], ':') >= 0 || !startsName(name[colon+1:])) {
		return 0, -1, end
	}
	return len(name), colon, end
}

// commitStartTag completes a start tag at d.buf[at:] whose attributes are in
// d.attrs: it checks the element against the document, applies namespace
// declarations and pushes the element. colon is the offset of the colon in
// name, or -1.
func (d *Decoder) commitStartTag(at int, name []byte, colon int, selfClose, hasDecl bool) error {
	if d.raw {
		d.kind = StartElement
		d.nameS, d.nameE, d.colon = at+1, at+1+len(name), colon
		d.selfEnd = selfClose
		d.rawDecl = hasDecl
		return nil
	}
	if len(d.stack) == 0 {
		if d.rootSeen && !d.frags {
			return d.syntaxErr(at, "multiple root elements")
		}
		d.rootSeen = true
	}
	if len(d.stack) >= d.opts.MaxDepth {
		return d.syntaxErr(at, "exceeded max depth")
	}

	entry := stackEntry{nameOff: len(d.nameArena), nameLen: len(name), colon: colon, nsMark: len(d.bindings)}
	if n := len(d.nameArena); len(name) <= 8 && cap(d.nameArena)-n >= len(name) {
		// short names: a byte loop beats the memmove call
		d.nameArena = d.nameArena[:n+len(name)]
		for k, c := range name {
			d.nameArena[n+k] = c
		}
	} else {
		d.nameArena = append(d.nameArena, name...)
	}
	if hasDecl {
		for k := range d.attrs {
			a := &d.attrs[k]
			if !a.decl {
				continue
			}
			var prefix string
			if a.colon >= 0 {
				prefix = d.internBytes(d.buf[a.name+a.colon+1 : a.nameEnd])
			}
			uri := string(d.attrValue(a))
			if !d.perm {
				if msg := badDecl(prefix, uri); msg != "" {
					return d.syntaxErr(a.name, msg)
				}
			}
			if prefix == "" {
				d.defaultNS = uri
			} else {
				d.addBinding(prefix, uri)
			}
		}
	}
	entry.defaultNS = d.defaultNS
	entry.nsChanged = hasDecl
	d.stack = append(d.stack, entry)

	// every prefix in use must be bound (permissive: the prefix is kept as Space)
	if !d.perm && colon >= 0 {
		if _, ok := d.lookup(name[:colon]); !ok {
			return d.syntaxErr(at+1, "unbound namespace prefix "+string(name[:colon]))
		}
	}
	prefixed := 0 // attributes with a bound, non-declaration prefix
	for k := 0; k < len(d.attrs) && !d.perm; k++ {
		a := &d.attrs[k]
		if a.colon < 0 || a.decl {
			continue
		}
		p := d.buf[a.name : a.name+a.colon]
		if _, ok := d.lookup(p); !ok {
			return d.syntaxErr(a.name, "unbound namespace prefix "+string(p))
		}
		prefixed++
	}
	if prefixed >= 2 {
		if err := d.duplicateExpanded(prefixed); err != nil {
			return err
		}
	}

	d.kind = StartElement
	d.nameS, d.nameE, d.colon = at+1, at+1+len(name), colon
	d.selfEnd = selfClose
	return nil
}

// firstAttrSpace is the index of the first literal tab, newline or carriage
// return in val[from:first] (to the end if first is -1), which attribute value
// normalization replaces; otherwise first. Bytes before from are known to hold none.
func firstAttrSpace(val []byte, from, first int) int {
	end := len(val)
	if first >= 0 {
		end = first
	}
	for i := from; i < end; i++ {
		if c := val[i]; c == '\t' || c == '\n' || c == '\r' {
			return i
		}
	}
	return first
}

// splitName finds the prefix separator. More than one colon is an error
// (reported with msg in the permissive profiles, which follow encoding/xml's
// wording); a leading or trailing colon is an error in the default profile
// (not a QName) and makes the whole string the local name in the permissive ones.
func (d *Decoder) splitName(n []byte, at int, msg string) (int, error) {
	c := bytes.IndexByte(n, ':')
	if c < 0 {
		return -1, nil
	}
	if bytes.IndexByte(n[c+1:], ':') >= 0 {
		if d.perm {
			return -1, d.syntaxErr(at, msg)
		}
		return -1, d.syntaxErr(at, "name "+string(n)+" has more than one colon")
	}
	if c == 0 || c == len(n)-1 {
		if !d.perm {
			if c == 0 {
				return -1, d.syntaxErr(at, "name "+string(n)+" has an empty prefix")
			}
			return -1, d.syntaxErr(at, "name "+string(n)+" has an empty local name")
		}
		return -1, nil
	}
	if !d.perm && !startsName(n[c+1:]) {
		return -1, d.syntaxErr(at, "name "+string(n)+" has a local name that cannot start a name")
	}
	return c, nil
}

// startsName reports whether b begins with a name start character.
func startsName(b []byte) bool {
	if b[0] < utf8.RuneSelf {
		return nameClass[b[0]] == 1
	}
	r, _ := utf8.DecodeRune(b)
	return isNameStartRune(r)
}

// lookup resolves a prefix to its URI.
func (d *Decoder) lookup(prefix []byte) (string, bool) {
	switch string(prefix) {
	case "xml":
		return XMLNamespace, true
	case "xmlns":
		return XMLNSNamespace, true
	}
	if d.nsMap != nil {
		if i, ok := d.nsMap[string(prefix)]; ok {
			return d.bindings[i].uri, true
		}
		return "", false
	}
	for i := len(d.bindings) - 1; i >= 0; i-- {
		if d.bindings[i].prefix == string(prefix) {
			return d.bindings[i].uri, true
		}
	}
	return "", false
}

const (
	// nsMapMin is the number of live namespace bindings above which lookup
	// switches from a backwards scan to a prefix index, and attrSetMin the
	// attribute count above which duplicate detection switches from pairwise
	// comparison to a set. Both keep hostile inputs (tens of thousands of
	// xmlns declarations or attributes) linear instead of quadratic.
	nsMapMin   = 16
	attrSetMin = 32
)

// addBinding records a namespace declaration, indexing it by prefix once
// there are many live bindings.
func (d *Decoder) addBinding(prefix, uri string) {
	b := binding{prefix: prefix, uri: uri, prev: -1}
	if d.nsMap != nil {
		if h, ok := d.nsMap[prefix]; ok {
			b.prev = h
		}
		d.nsMap[prefix] = len(d.bindings)
	}
	d.bindings = append(d.bindings, b)
	if d.nsMap == nil && len(d.bindings) > nsMapMin {
		d.nsMap = make(map[string]int, 2*len(d.bindings))
		for i := range d.bindings {
			bb := &d.bindings[i]
			bb.prev = -1
			if h, ok := d.nsMap[bb.prefix]; ok {
				bb.prev = h
			}
			d.nsMap[bb.prefix] = i
		}
	}
}

// truncBindings drops the bindings declared after mark (a closed element's).
func (d *Decoder) truncBindings(mark int) {
	if d.nsMap != nil {
		for i := len(d.bindings) - 1; i >= mark; i-- {
			if b := &d.bindings[i]; b.prev < 0 {
				delete(d.nsMap, b.prefix)
			} else {
				d.nsMap[b.prefix] = b.prev
			}
		}
	}
	d.bindings = d.bindings[:mark]
}

// duplicateAttrSet is the duplicate check for tags with many attributes: it
// reports whether an equals an already recorded attribute name and otherwise
// remembers it, in a set built the first time a tag reaches attrSetMin.
func (d *Decoder) duplicateAttrSet(an []byte) bool {
	if !d.attrSetOn {
		if d.attrSet == nil {
			d.attrSet = make(map[string]struct{}, 2*len(d.attrs))
		} else {
			clear(d.attrSet)
		}
		for k := range d.attrs {
			d.attrSet[string(d.buf[d.attrs[k].name:d.attrs[k].nameEnd])] = struct{}{}
		}
		d.attrSetOn = true
	}
	if _, dup := d.attrSet[string(an)]; dup {
		return true
	}
	d.attrSet[string(an)] = struct{}{}
	return false
}

// badDecl explains a namespace declaration the default profile refuses
// (Namespaces in XML 1.0, "Reserved Prefixes and Namespace Names"), or returns "".
// The permissive profiles accept all of them, as encoding/xml does.
func badDecl(prefix, uri string) string {
	switch {
	case prefix == "xmlns":
		return "reserved namespace prefix xmlns cannot be declared"
	case uri == XMLNSNamespace:
		return "reserved namespace " + XMLNSNamespace + " cannot be bound"
	case prefix == "xml":
		if uri != XMLNamespace {
			return "prefix xml must be bound to " + XMLNamespace
		}
	case uri == XMLNamespace:
		return "namespace " + XMLNamespace + " can only be bound to prefix xml"
	case prefix != "" && uri == "":
		return "xmlns:" + prefix + " declared with an empty namespace"
	}
	return ""
}

// duplicateExpanded reports two attributes of the current tag whose prefixes
// differ but resolve to the same (namespace, local name). n is the number of
// attributes with a bound prefix; the other attributes cannot collide with
// them. Called only for n >= 2, so tags without prefixed attributes skip it.
func (d *Decoder) duplicateExpanded(n int) error {
	local := func(a *attrSpan) []byte { return d.buf[a.name+a.colon+1 : a.nameEnd] }
	qname := func(a *attrSpan) string { return string(d.buf[a.name:a.nameEnd]) }
	if n >= attrSetMin {
		if d.expSet == nil {
			d.expSet = make(map[string]string, 2*n)
		} else {
			clear(d.expSet)
		}
		for k := range d.attrs {
			a := &d.attrs[k]
			if a.colon < 0 || a.decl {
				continue
			}
			uri, _ := d.lookup(d.buf[a.name : a.name+a.colon])
			key := uri + "\x00" + string(local(a))
			if prev, dup := d.expSet[key]; dup {
				return d.syntaxErr(a.name, "duplicate attribute "+qname(a)+" (same name as "+prev+")")
			}
			d.expSet[key] = qname(a)
		}
		return nil
	}
	for k := range d.attrs {
		a := &d.attrs[k]
		if a.colon < 0 || a.decl {
			continue
		}
		for j := 0; j < k; j++ {
			b := &d.attrs[j]
			if b.colon < 0 || b.decl || !bytes.Equal(local(a), local(b)) {
				continue
			}
			ua, _ := d.lookup(d.buf[a.name : a.name+a.colon])
			if ub, _ := d.lookup(d.buf[b.name : b.name+b.colon]); ua == ub {
				return d.syntaxErr(a.name, "duplicate attribute "+qname(a)+" (same name as "+qname(b)+")")
			}
		}
	}
	return nil
}

// attrValue returns the resolved value bytes of an attribute span.
func (d *Decoder) attrValue(a *attrSpan) []byte {
	if a.verbatim {
		return d.buf[a.val:a.valEnd]
	}
	return d.attrBuf[a.val:a.valEnd]
}

func clip(b []byte) []byte {
	if len(b) > 16 {
		return b[:16]
	}
	return b
}

func (d *Decoder) appendRef(dst, ref []byte, at int) ([]byte, bool) {
	if len(ref) == 0 {
		return dst, false
	}
	if ref[0] == '#' {
		var n uint32
		digits := ref[1:]
		base := uint32(10)
		if len(digits) > 0 && digits[0] == 'x' {
			base, digits = 16, digits[1:]
		}
		if len(digits) == 0 {
			return dst, false
		}
		if !d.perm {
			// leading zeros do not count toward the limit that keeps n from overflowing
			z := 0
			for z < len(digits) && digits[z] == '0' {
				z++
			}
			if digits = digits[z:]; len(digits) > 8 {
				return dst, false
			}
			if len(digits) == 0 { // U+0000
				return dst, false
			}
		}
		over := false
		for _, c := range digits {
			var v uint32
			switch {
			case c >= '0' && c <= '9':
				v = uint32(c - '0')
			case base == 16 && c >= 'a' && c <= 'f':
				v = uint32(c-'a') + 10
			case base == 16 && c >= 'A' && c <= 'F':
				v = uint32(c-'A') + 10
			default:
				return dst, false
			}
			if n > (1<<32-1-v)/base {
				over = true
			}
			n = n*base + v
		}
		if d.perm {
			// encoding/xml: a number above U+10FFFF is not a reference; a
			// surrogate becomes U+FFFD; anything else must be an XML Char.
			if over || n > 0x10FFFF {
				return dst, false
			}
			if n >= 0xD800 && n <= 0xDFFF {
				n = 0xFFFD
			}
			if !validXMLRune(rune(n)) {
				d.illegalRef = true
				d.illegalRune = rune(n)
				return dst, false
			}
			return utf8Append(dst, rune(n)), true
		}
		if !validXMLRune(rune(n)) {
			return dst, false
		}
		return utf8Append(dst, rune(n)), true
	}
	if c, ok := predefinedRef(ref); ok {
		return append(dst, c), true
	}
	// Like encoding/xml, validate the profile's Name before the entity-map lookup.
	if !d.validName(ref) {
		return dst, false
	}
	if d.opts.LegacyDecoder != nil {
		if value, found, saved := d.legacyReferenceAt(at); saved {
			if found {
				return append(dst, value...), true
			}
			return dst, false
		}
	}
	if v, ok := d.opts.Entities[string(ref)]; ok {
		return append(dst, v...), true
	}
	return dst, false
}

func predefinedRef(ref []byte) (byte, bool) {
	switch string(ref) {
	case "lt":
		return '<', true
	case "gt":
		return '>', true
	case "amp":
		return '&', true
	case "apos":
		return '\'', true
	case "quot":
		return '"', true
	}
	return 0, false
}

// pushBare handles a start tag the engine verified to be "<name>" or
// "<name/>" with a plain ASCII, colon-free name and no attributes.
func (d *Decoder) pushBare(bd boundary) error {
	at := bd.start
	limit := bd.end - 1
	selfClose := d.buf[limit-1] == '/'
	if selfClose {
		limit--
	}
	name := d.buf[at+1 : limit]
	d.attrs = d.attrs[:0]
	if d.raw {
		d.kind = StartElement
		d.nameS, d.nameE, d.colon = at+1, at+1+len(name), -1
		d.selfEnd = selfClose
		return nil
	}
	if len(d.stack) == 0 {
		if d.rootSeen && !d.frags {
			return d.syntaxErr(at, "multiple root elements")
		}
		d.rootSeen = true
	}
	if len(d.stack) >= d.opts.MaxDepth {
		return d.syntaxErr(at, "exceeded max depth")
	}
	n := len(d.nameArena)
	if len(name) <= 8 && cap(d.nameArena)-n >= len(name) {
		d.nameArena = d.nameArena[:n+len(name)]
		for k, c := range name {
			d.nameArena[n+k] = c
		}
	} else {
		d.nameArena = append(d.nameArena, name...)
	}
	d.stack = append(d.stack, stackEntry{nameOff: n, nameLen: len(name), colon: -1, nsMark: len(d.bindings), defaultNS: d.defaultNS})
	d.kind = StartElement
	d.nameS, d.nameE, d.colon = at+1, at+1+len(name), -1
	d.selfEnd = selfClose
	return nil
}

// badStartName explains a start tag whose name is not a valid name. The
// permissive profiles use encoding/xml's reading: the name is the longest run
// of name bytes, and anything else that follows is reported as a missing
// attribute name.
func (d *Decoder) badStartName(tag []byte) string {
	const elem = "expected element name after <"
	if !d.perm {
		return elem
	}
	j := 1
	for j < len(tag) && (isNameByte(tag[j]) || tag[j] >= 0x80) {
		j++
	}
	run := tag[1:j]
	if len(run) == 0 {
		return elem
	}
	if !d.validName(run) {
		return "invalid XML name: " + string(run)
	}
	if bytes.Count(run, []byte{':'}) > 1 {
		return elem
	}
	return "expected attribute name in element"
}

func isNameByte(c byte) bool {
	return 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' ||
		c == '_' || c == ':' || c == '.' || c == '-'
}

// rawSplit splits a qualified name like encoding/xml's RawToken does.
func rawSplit(q []byte) (space, local string) {
	c := bytes.IndexByte(q, ':')
	if c <= 0 || c == len(q)-1 || bytes.IndexByte(q[c+1:], ':') >= 0 {
		return "", string(q)
	}
	return string(q[:c]), string(q[c+1:])
}

// mismatchMsg is the end-tag mismatch error in encoding/xml's words.
func mismatchMsg(open, name []byte) string {
	os, ol := rawSplit(open)
	ns, nl := rawSplit(name)
	if ol != nl {
		return "element <" + ol + "> closed by </" + nl + ">"
	}
	if ns == "" {
		ns = `""`
	}
	return "element <" + ol + "> in space " + os + " closed by </" + nl + "> in space " + ns
}

// badAttrName explains an invalid attribute name; b starts at it. The
// permissive profiles read it like encoding/xml: a run of name bytes, then '='.
func (d *Decoder) badAttrName(b []byte) string {
	const msg = "expected attribute name in element"
	if !d.perm {
		return msg
	}
	j := 0
	for j < len(b) && (isNameByte(b[j]) || b[j] >= 0x80) {
		j++
	}
	switch {
	case j == 0:
		return msg
	case !d.validName(b[:j]):
		return "invalid XML name: " + string(b[:j])
	case bytes.Count(b[:j], []byte{':'}) > 1:
		return msg
	}
	return "attribute name without = in element"
}

// rawStartErr replaces the error of a start tag by the one encoding/xml's lexer
// would report, for the tokenizer mode the v1 layer uses (RawNesting).
func (d *Decoder) rawStartErr(err error, bd boundary) error {
	if e2 := d.v1StartTagErr(d.buf[bd.start:bd.end], bd.start, false); e2 != nil {
		return e2
	}
	return err
}
