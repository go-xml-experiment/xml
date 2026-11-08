// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import "unicode/utf8"

func utf8Append(dst []byte, r rune) []byte { return utf8.AppendRune(dst, r) }

// Token is a view of the decoder's current token. It is a small value; its
// accessors read decoder state, so it is invalidated by the next Decoder
// call. Use Clone for a copy that outlives the view.
type Token struct {
	d    *Decoder
	kind Kind
	own  *ownedToken
}

type ownedToken struct {
	name    Name
	raw     Name // lexical name, as RawName reports it
	attrs   []ownedAttr
	text    []byte
	partial bool
}

// ownedAttr is a cloned attribute: the resolved Attr and its lexical name.
type ownedAttr struct {
	Attr
	raw Name
}

// Kind returns the token kind.
func (t Token) Kind() Kind { return t.kind }

// Partial reports, for a Text or CData token read with SplitText, that the
// next token is a further piece of the same Text run or the same CDATA
// section; it is false for the last piece. Two adjacent CDATA sections are
// told apart: the first ends with Partial false. Always false without
// SplitText and for other kinds.
func (t Token) Partial() bool {
	if t.own != nil {
		return t.own.partial
	}
	return t.d != nil && t.d.partial && (t.kind == Text || t.kind == CData)
}

// Name returns the namespace-resolved name of a StartElement or EndElement,
// or the target of a ProcInst (in Local).
func (t Token) Name() Name {
	if t.own != nil {
		return t.own.name
	}
	d := t.d
	switch t.kind {
	case StartElement, EndElement:
		e := &d.stack[len(d.stack)-1]
		if d.rawEnd {
			q := d.nameArena[e.nameOff : e.nameOff+e.nameLen]
			if e.colon < 0 {
				return Name{Local: d.internBytes(q)}
			}
			return Name{Space: d.internBytes(q[:e.colon]), Local: d.internBytes(q[e.colon+1:])}
		}
		if e.colon < 0 {
			if e.local == "" {
				e.local = d.internBytes(d.nameArena[e.nameOff : e.nameOff+e.nameLen])
			}
			return Name{Space: e.defaultNS, Local: e.local}
		}
		q := d.nameArena[e.nameOff : e.nameOff+e.nameLen]
		if e.local == "" {
			e.local = d.internBytes(q[e.colon+1:])
		}
		return Name{Space: d.spaceOf(q[:e.colon], true), Local: e.local}
	case ProcInst:
		return Name{Local: d.internBytes(d.buf[d.nameS:d.nameE])}
	}
	return Name{}
}

// spaceOf resolves a prefix to its URI. An unbound prefix (possible only in
// permissive mode) stands for itself, as in encoding/xml; so does the
// prefix xmlns on an element name. A permissive attribute prefix bound to the
// xmlns namespace stands for itself as well: that Space is reserved to
// declarations (an element name cannot be mistaken for one, so it keeps the URI).
func (d *Decoder) spaceOf(prefix []byte, element bool) string {
	if element && d.perm && string(prefix) == "xmlns" {
		return "xmlns"
	}
	if uri, ok := d.lookup(prefix); ok && !(d.perm && d.opts.LegacyDecoder == nil && !element && uri == XMLNSNamespace) {
		return uri
	}
	return d.internBytes(prefix)
}

// Text returns the content of a Text, CData, Comment, ProcInst (data) or
// Directive token, with references resolved. The slice is read-only.
func (t Token) Text() []byte {
	if t.own != nil {
		return t.own.text
	}
	d := t.d
	switch t.kind {
	case Text:
		if d.verbatim {
			return d.buf[d.tokS:d.tokE]
		}
		return d.scratch
	case CData:
		if d.verbatim {
			return d.buf[d.tokS:d.tokE]
		}
		return d.scratch
	case Directive:
		if d.verbatim {
			return d.buf[d.tokS:d.tokE]
		}
		return d.scratch
	case Comment, ProcInst:
		return d.buf[d.tokS:d.tokE]
	}
	return nil
}

// Attrs iterates the attributes of a StartElement, including namespace
// declarations. A declaration is named by XMLNSNamespace, as in the DOM:
// xmlns:p="u" is {Space: XMLNSNamespace, Local: "p"} and xmlns="u" is
// {Space: XMLNSNamespace, Local: "xmlns"}. Attributes in XMLNamespace (xml:lang
// and the like) are ordinary. No other attribute has the Space XMLNSNamespace.
func (t Token) Attrs() AttrIter {
	if t.own != nil {
		return AttrIter{own: t.own.attrs, i: -1}
	}
	if t.kind != StartElement {
		return AttrIter{i: -1}
	}
	return AttrIter{d: t.d, i: -1}
}

// AttrIter walks attributes without allocating.
type AttrIter struct {
	d   *Decoder
	own []ownedAttr
	i   int
}

// Len returns the number of attributes.
func (it AttrIter) Len() int {
	if it.d != nil {
		return len(it.d.attrs)
	}
	return len(it.own)
}

// Next advances to the next attribute and reports whether there is one.
func (it *AttrIter) Next() bool {
	it.i++
	return it.i < it.Len()
}

// Name returns the current attribute's resolved name. A namespace
// declaration has Space XMLNSNamespace (see Token.Attrs), and the xml prefix
// resolves to XMLNamespace; an unbound prefix, possible only in permissive mode, stands for itself.
func (it *AttrIter) Name() Name {
	if it.d == nil {
		return it.own[it.i].Name
	}
	d := it.d
	a := &d.attrs[it.i]
	q := d.buf[a.name:a.nameEnd]
	if a.decl {
		if d.opts.LegacyDecoder != nil {
			if a.colon < 0 {
				return Name{Local: "xmlns"}
			}
			return Name{Space: "xmlns", Local: d.internBytes(q[a.colon+1:])}
		}
		if a.colon < 0 {
			return Name{Space: XMLNSNamespace, Local: "xmlns"}
		}
		if l := d.internBytes(q[a.colon+1:]); l != "xmlns" {
			return Name{Space: XMLNSNamespace, Local: l}
		}
		// permissive xmlns:xmlns: not the default declaration
		return Name{Space: "xmlns", Local: "xmlns"}
	}
	if a.colon < 0 {
		return Name{Local: d.internBytes(q)}
	}
	return Name{Space: d.spaceOf(q[:a.colon], false), Local: d.internBytes(q[a.colon+1:])}
}

// isDecl reports whether the current attribute is a namespace declaration.
func (it *AttrIter) isDecl() bool {
	if it.d == nil {
		return it.own[it.i].Name.Space == XMLNSNamespace
	}
	return it.d.attrs[it.i].decl
}

// Value returns the current attribute's resolved value. Read-only.
func (it *AttrIter) Value() []byte {
	if it.d == nil {
		return []byte(it.own[it.i].Value)
	}
	return it.d.attrValue(&it.d.attrs[it.i])
}

// Clone returns a Token that owns its data and stays valid indefinitely. The
// clone reports the same Name, RawName and attribute names (resolved and
// lexical) as the original.
func (t Token) Clone() Token {
	if t.own != nil {
		return t
	}
	o := &ownedToken{name: t.Name(), raw: t.RawName(), text: append([]byte(nil), t.Text()...), partial: t.Partial()}
	for it := t.Attrs(); it.Next(); {
		o.attrs = append(o.attrs, ownedAttr{Attr{it.Name(), string(it.Value())}, it.RawName()})
	}
	return Token{kind: t.kind, own: o}
}

// RawName returns the lexical name of a StartElement, EndElement or ProcInst:
// the prefix (not its namespace URI) in Space and the local part in Local.
// It is what encoding/xml's RawToken reports; it is meant for the v1
// compatibility layer, which redoes namespace translation itself.
func (t Token) RawName() Name {
	if t.own != nil {
		return t.own.raw
	}
	d := t.d
	switch t.kind {
	case StartElement, EndElement:
		if d.raw {
			q := d.buf[d.nameS:d.nameE]
			if d.colon < 0 {
				return Name{Local: d.internBytes(q)}
			}
			return Name{Space: d.internBytes(q[:d.colon]), Local: d.internBytes(q[d.colon+1:])}
		}
		e := &d.stack[len(d.stack)-1]
		q := d.nameArena[e.nameOff : e.nameOff+e.nameLen]
		if d.rawEnd {
			if e.colon < 0 {
				return Name{Local: d.internBytes(q)}
			}
			return Name{Space: d.internBytes(q[:e.colon]), Local: d.internBytes(q[e.colon+1:])}
		}
		// The start and end tokens of an element share the stack entry, so the
		// end tag reuses the local name interned for the start tag.
		if e.local == "" {
			e.local = d.internBytes(q[e.colon+1:])
		}
		if e.colon < 0 {
			return Name{Local: e.local}
		}
		return Name{Space: d.internBytes(q[:e.colon]), Local: e.local}
	case ProcInst:
		return Name{Local: d.internBytes(d.buf[d.nameS:d.nameE])}
	}
	return Name{}
}

// RawName returns the current attribute's lexical name: the prefix in Space
// ("xmlns" for declarations of a prefix) and the local part in Local.
func (it *AttrIter) RawName() Name {
	if it.d == nil {
		return it.own[it.i].raw
	}
	d := it.d
	a := &d.attrs[it.i]
	q := d.buf[a.name:a.nameEnd]
	if a.colon < 0 {
		return Name{Local: d.internBytes(q)}
	}
	return Name{Space: d.internBytes(q[:a.colon]), Local: d.internBytes(q[a.colon+1:])}
}
