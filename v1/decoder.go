// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
	"github.com/Clickin/go-experiment-xml/xmltext"
)

// NewDecoder creates a new XML parser reading from r.
// If r does not implement [io.ByteReader], NewDecoder will
// do its own buffering.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{
		ns:     make(map[string]string),
		src:    r,
		Strict: true,
	}
}

// NewTokenDecoder creates a new XML parser using an underlying token stream.
func NewTokenDecoder(t TokenReader) *Decoder {
	// Is it already a Decoder?
	if d, ok := t.(*Decoder); ok {
		return d
	}
	return &Decoder{
		ns:     make(map[string]string),
		t:      t,
		Strict: true,
	}
}

// byteAtATime feeds a ByteReader to the tokenizer one byte per Read, so the
// consumed prefix of the caller's reader is exactly what encoding/xml consumed.
type byteAtATime struct{ br io.ByteReader }

func (b byteAtATime) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c, err := b.br.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = c
	return 1, nil
}

// chunked caps the size of each Read like encoding/xml's 4096-byte bufio.Reader.
type chunked struct{ r io.Reader }

func (c chunked) Read(p []byte) (int, error) {
	if len(p) > 4096 {
		p = p[:4096]
	}
	return c.r.Read(p)
}

// open creates the tokenizer; mutable public settings remain owned by d.
func (d *Decoder) open() {
	d.settings = xmlopts.LegacyDecoder{Strict: &d.Strict, Entity: &d.Entity, CharsetReader: &d.CharsetReader}
	opts := []xmltext.Options{
		xmltext.MaxTokenSize(0), xmltext.MaxAttributes(0),
		xmlopts.RawNesting(true), xmltext.Permissive(true),
		xmlopts.NewOption(xmlopts.Options{LegacyDecoder: &d.settings}),
	}
	if d.data != nil {
		d.in = dataSource{d.data}
	} else {
		d.in = newSource(d.src)
	}
	d.xd, d.spare = d.spare, nil // a pooled Decoder brings its tokenizer along
	if d.xd == nil {
		d.xd = new(xmltext.Decoder)
	}
	d.setStringCache(d.names)
	if data := d.in.bytes(); data != nil {
		d.xd.ResetBytes(data, opts...)
	} else {
		d.xd.Reset(d.in.reader(), opts...)
	}
	if d.saving {
		d.xd.StartCapture()
	}
}

func (d *Decoder) convertErr(err error) error {
	if se, ok := err.(*xmltext.SyntaxError); ok {
		// encoding/xml reports declaration problems as plain errors
		if v, ok := strings.CutPrefix(se.Msg, "unsupported version: "); ok {
			v, _, _ = strings.Cut(v, ";")
			return fmt.Errorf("xml: unsupported version %q; only version 1.0 is supported", v)
		}
		if enc, ok := strings.CutPrefix(se.Msg, "unsupported encoding: "); ok {
			return fmt.Errorf("xml: encoding %q declared but Decoder.CharsetReader is nil", enc)
		}
		return &SyntaxError{Msg: se.Msg, Line: se.Line}
	}
	return err
}

func (d *Decoder) line() int {
	if d.xd == nil {
		return 1
	}
	l, _ := d.xd.InputPos()
	return l
}

// rawKind tells which field of a raw token result is set.
type rawKind uint8

const (
	rawNone  rawKind = iota // no token; err is set
	rawStart                // start element in st
	rawEnd                  // end element named st.Name
	rawOther                // any other token in tok
)

// rawToken returns the next token without namespace translation or end tag
// matching beyond what the tokenizer itself does.
func (d *Decoder) rawToken() (Token, error) {
	var st StartElement
	k, tok, err := d.readRaw(&st, false)
	switch k {
	case rawStart:
		return st, err
	case rawEnd:
		return EndElement{st.Name}, err
	}
	return tok, err
}

// readRaw is rawToken without boxing start and end elements into a Token,
// so that Token can translate names and box the result only once. It
// stores a start element, or the name of an end element, in st.
// If lazy, a start element read by the tokenizer gets no Attr: its
// attributes stay in the tokenizer (see startTok) until the next read, and
// d.lazyStart is set.
func (d *Decoder) readRaw(st *StartElement, lazy bool) (k rawKind, tok Token, err error) {
	if d.t != nil {
		tok, err = d.t.Token()
		switch t := tok.(type) {
		case nil:
			return rawNone, nil, err
		case StartElement:
			*st = t
			return rawStart, nil, err
		case EndElement:
			*st = StartElement{Name: t.Name}
			return rawEnd, nil, err
		}
		return rawOther, tok, err
	}
	if d.err != nil {
		return rawNone, nil, d.err
	}
	if d.needClose {
		// The last element we read was self-closing and
		// we returned just the StartElement half.
		// Return the EndElement half now.
		d.needClose = false
		*st = StartElement{Name: d.toClose}
		return rawEnd, nil, nil
	}
	if d.xd == nil {
		d.open()
	}
	textEnd := false
	defer func() { d.in.commit(d.xd.InputOffset(), textEnd && err == nil) }()
	tk, err := d.xd.ReadToken()
	if err != nil {
		return rawNone, nil, d.convertErr(err)
	}
	switch tk.Kind() {
	case xmltext.StartElement:
		n := tk.RawName()
		*st = StartElement{Name: Name{Space: n.Space, Local: n.Local}}
		if lazy {
			d.lazyStart = true
		} else {
			st.Attr = rawAttrs(tk)
		}
		return rawStart, nil, nil
	case xmltext.EndElement:
		n := tk.RawName()
		*st = StartElement{Name: Name{Space: n.Space, Local: n.Local}}
		return rawEnd, nil, nil
	case xmltext.Text:
		textEnd = true
		return rawOther, CharData(tk.Text()), nil
	case xmltext.CData:
		return rawOther, CharData(tk.Text()), nil
	case xmltext.Comment:
		return rawOther, Comment(tk.Text()), nil
	case xmltext.ProcInst:
		return rawOther, ProcInst{Target: tk.RawName().Local, Inst: tk.Text()}, nil
	case xmltext.Directive:
		return rawOther, Directive(tk.Text()), nil
	}
	return rawNone, nil, d.syntaxError("unexpected token")
}

// rawAttrs returns the attributes of the start element tk, with their
// names untranslated.
func rawAttrs(tk xmltext.Token) []Attr {
	it := tk.Attrs()
	attr := make([]Attr, 0, it.Len())
	for it.Next() {
		an := it.RawName()
		attr = append(attr, Attr{Name{Space: an.Space, Local: an.Local}, string(it.Value())})
	}
	return attr
}

// InputOffset returns the input stream byte offset of the current decoder position.
// The offset gives the location of the end of the most recently returned token
// and the beginning of the next token.
func (d *Decoder) InputOffset() int64 {
	if d.xd == nil {
		return 0
	}
	return d.xd.InputOffset()
}

// InputPos returns the line of the current decoder position and the 1 based
// input position of the line. The position gives the location of the end of the
// most recently returned token.
func (d *Decoder) InputPos() (line, column int) {
	if d.xd == nil {
		return 1, 1
	}
	return d.xd.InputPos()
}

// Raw input capture for ,innerxml: the bytes consumed since startSaved.

func (d *Decoder) startSaved() {
	d.saving = true
	if d.xd != nil {
		d.xd.StartCapture()
	}
}

func (d *Decoder) savedOffset() int {
	if d.xd == nil {
		return 0
	}
	return d.xd.CaptureLen()
}

func (d *Decoder) savedBytes(from, to int) []byte {
	if d.xd == nil || to <= from {
		return nil
	}
	return d.xd.Captured(from, to)
}

func (d *Decoder) stopSaved() {
	d.saving = false
	if d.xd != nil {
		d.xd.StopCapture()
	}
}

var _ = bytes.NewReader

var stringCachePool = sync.Pool{New: func() any { return new([256]string) }}

// setStringCache sets the string cache of the tokenizer, if open.
func (d *Decoder) setStringCache(c *[256]string) {
	if d.xd != nil {
		*xmltext.Internal.Export(&xmlopts.AllowInternalUse).StringCache(d.xd) = c
	}
}
