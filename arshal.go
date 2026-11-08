// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"errors"
	"io"
	"reflect"
	"sync"
	"unsafe"

	"github.com/go-xml-experiment/xml/internal/xmllegacy"
	"github.com/go-xml-experiment/xml/internal/xmlopts"
	"github.com/go-xml-experiment/xml/xmltext"
)

var export = xmltext.Internal.Export(&xmlopts.AllowInternalUse)

// maxPooled is the largest buffer a pooled state keeps, as in
// encoding/json/v2.
const maxPooled = 64 << 10

// marshalState is the state of one marshal call. A MarshalerTo method or
// MarshalToFunc function finds it through its Encoder.
type marshalState struct {
	enc        *xmltext.Encoder
	opts       xmlopts.Options
	marshalers *Marshalers
	buf        []byte // scratch for text; see takeBuf
	elemSpace  string // namespace of the element whose content is being written
	qual       bool   // names without a namespace take elemSpace; see structFields.qualified

	legacyState // for encoding/xml; see arshal_legacy.go
}

// unmarshalState is the state of one unmarshal call.
type unmarshalState struct {
	src          xmllegacy.TokenSource // what tokens are read from; usually &tsrc
	tsrc         xmltextSource
	dec          *xmltext.Decoder // nil if src is not an xmltext.Decoder
	opts         xmlopts.Options
	unmarshalers *Unmarshalers
	bufs         [][]byte // free scratch buffers; see takeBuf
	captures     int      // structs with an innerxml field being read

	legacy bool             // encoding/xml semantics; see arshal_legacy_read.go
	hooks  *xmllegacy.Hooks // if legacy

	elemSpace string // namespace of the element whose content is being read
	qual      bool   // names without a namespace take elemSpace; see structFields.qualified
}

func (ms *marshalState) init(enc *xmltext.Encoder, o xmlopts.Options) {
	ms.enc, ms.opts = enc, o
	ms.marshalers, _ = o.Marshalers.(*Marshalers)
}

func (us *unmarshalState) init(dec *xmltext.Decoder, o xmlopts.Options) {
	us.dec, us.opts = dec, o
	us.tsrc = xmltextSource{dec: dec}
	us.src = &us.tsrc
	us.unmarshalers, _ = o.Unmarshalers.(*Unmarshalers)
}

// takeBuf and putBuf lend scratch buffers; text read for one element may
// be pending while its children use buffers of their own.
func (us *unmarshalState) takeBuf() []byte {
	if n := len(us.bufs); n > 0 {
		b := us.bufs[n-1]
		us.bufs = us.bufs[:n-1]
		return b[:0]
	}
	return nil
}

func (us *unmarshalState) putBuf(b []byte) {
	if cap(b) > 0 && cap(b) <= maxPooled {
		us.bufs = append(us.bufs, b)
	}
}

// encoderState is a pooled marshal state with its output buffer.
type encoderState struct {
	enc xmltext.Encoder
	buf bytes.Buffer
	ms  marshalState
}

var encoderPool sync.Pool // of *encoderState

func getEncoderState(w io.Writer, opts []Options) *encoderState {
	st, _ := encoderPool.Get().(*encoderState)
	if st == nil {
		st = new(encoderState)
	}
	if w == nil {
		w = &st.buf
	}
	st.enc.Reset(w, opts...)
	st.ms.init(&st.enc, xmlopts.Join(opts...))
	*export.EncoderState(&st.enc) = &st.ms
	return st
}

func putEncoderState(st *encoderState) {
	st.enc.Release()
	if st.buf.Cap() > maxPooled {
		st.buf = bytes.Buffer{}
	}
	st.buf.Reset()
	if cap(st.ms.buf) > maxPooled {
		st.ms.buf = nil
	}
	st.ms = marshalState{buf: st.ms.buf[:0]}
	encoderPool.Put(st)
}

// decoderState is a pooled unmarshal state with its Decoder.
type decoderState struct {
	dec   xmltext.Decoder
	us    unmarshalState
	names [256]string // the Decoder's string cache
}

var decoderPool sync.Pool // of *decoderState

func getDecoderState(in []byte, r io.Reader, opts []Options) *decoderState {
	st, _ := decoderPool.Get().(*decoderState)
	if st == nil {
		st = new(decoderState)
	}
	if r != nil {
		st.dec.Reset(r, opts...)
	} else {
		st.dec.ResetBytes(in, opts...)
	}
	*export.StringCache(&st.dec) = &st.names
	st.us.init(&st.dec, xmlopts.Join(opts...))
	*export.DecoderState(&st.dec) = &st.us
	return st
}

func putDecoderState(st *decoderState) {
	st.dec.Release()
	bufs := st.us.bufs
	clear(bufs[:cap(bufs)])
	st.us = unmarshalState{bufs: bufs[:0]}
	decoderPool.Put(st)
}

// Marshal returns the XML encoding of in.
//
// The top-level value is an element named by its XMLName field or else by
// its type name. See the package documentation for how values are mapped.
func Marshal(in any, opts ...Options) (out []byte, err error) {
	st := getEncoderState(nil, opts)
	defer putEncoderState(st)
	if err := marshalEncode(&st.ms, in); err != nil {
		return nil, err
	}
	if err := st.enc.Flush(); err != nil {
		return nil, err
	}
	return bytes.Clone(st.buf.Bytes()), nil
}

// MarshalWrite writes the XML encoding of in to out.
func MarshalWrite(out io.Writer, in any, opts ...Options) (err error) {
	st := getEncoderState(out, opts)
	defer putEncoderState(st)
	if err := marshalEncode(&st.ms, in); err != nil {
		return err
	}
	return st.enc.Flush()
}

// MarshalEncode writes the XML encoding of in to out as one element. Within
// a [MarshalerTo] method or [MarshalToFunc] function, it writes a child of
// the element being marshaled, with the options of the enclosing call
// followed by opts.
func MarshalEncode(out *xmltext.Encoder, in any, opts ...Options) (err error) {
	slot := export.EncoderState(out)
	prev, _ := (*slot).(*marshalState)
	if prev != nil && prev.legacy {
		prev = nil // the state of encoding/xml; see legacyMarshal
	}
	if prev != nil && len(opts) == 0 {
		return marshalEncode(prev, in)
	}
	ms := new(marshalState)
	o := xmlopts.Join(opts...)
	if prev != nil {
		o = xmlopts.Join(xmlopts.NewOption(prev.opts), xmlopts.NewOption(o))
	}
	ms.init(out, o)
	saved := *slot
	*slot = ms
	defer func() { *slot = saved }()
	return marshalEncode(ms, in)
}

func marshalEncode(ms *marshalState, in any) error {
	v := reflect.ValueOf(in)
	if !v.IsValid() || (v.Kind() == reflect.Pointer && v.IsNil()) {
		return &SemanticError{action: "marshal", Err: errors.New("nil value")}
	}
	// Shallow copy non-pointer values to obtain an addressable value.
	// It is beneficial to performance to always pass pointers to avoid this.
	forceAddr := v.Kind() != reflect.Pointer
	if forceAddr {
		v2 := reflect.New(v.Type())
		v2.Elem().Set(v)
		v = v2
	}
	va := addressableValue{v.Elem(), forceAddr}
	// The zero name lets the value name itself; see elementName.
	return ms.marshalValue(xmltext.Name{}, va)
}

// Unmarshal decodes the XML document in into out, which must be a non-nil
// pointer. Prolog and trailing comments, processing instructions and
// whitespace are skipped.
func Unmarshal(in []byte, out any, opts ...Options) (err error) {
	st := getDecoderState(in, nil, opts)
	defer putDecoderState(st)
	return unmarshalDecode(&st.us, out, true)
}

// UnmarshalRead decodes the XML document read from in into out.
func UnmarshalRead(in io.Reader, out any, opts ...Options) (err error) {
	st := getDecoderState(nil, in, opts)
	defer putDecoderState(st)
	return unmarshalDecode(&st.us, out, true)
}

// UnmarshalDecode decodes the next element read from in into out, skipping
// the character data, comments and other tokens that precede it. It returns
// io.EOF, without reading it, at the end tag of the enclosing element or at
// the end of the input. Within an
// [UnmarshalerFrom] method or [UnmarshalFromFunc] function, it reads a child
// of the element being unmarshaled, with the options of the enclosing call
// followed by opts.
func UnmarshalDecode(in *xmltext.Decoder, out any, opts ...Options) (err error) {
	slot := export.DecoderState(in)
	prev, _ := (*slot).(*unmarshalState)
	if prev != nil && len(opts) == 0 {
		return unmarshalDecode(prev, out, false)
	}
	us := new(unmarshalState)
	o := xmlopts.Join(opts...)
	if prev != nil {
		o = xmlopts.Join(xmlopts.NewOption(prev.opts), xmlopts.NewOption(o))
	}
	us.init(in, o)
	*slot = us
	defer func() { *slot = prev }()
	return unmarshalDecode(us, out, false)
}

var errNonPointer = errors.New("value must be a non-nil pointer")

func unmarshalDecode(us *unmarshalState, out any, whole bool) error {
	v := reflect.ValueOf(out)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return &SemanticError{action: "unmarshal", GoType: reflect.TypeOf(out), Err: errNonPointer}
	}
	src := us.src
	for {
		if src.PeekKind() == xmltext.EndElement {
			return io.EOF
		}
		k, err := us.next()
		if err != nil {
			if whole && err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		if k == xmltext.StartElement {
			break
		}
		if k == xmltext.EndElement {
			return &SemanticError{action: "unmarshal", ByteOffset: src.Offset(), Err: errors.New("unexpected end element")}
		}
	}
	va := addressableValue{v.Elem(), false}
	if err := us.unmarshalValue(va); err != nil {
		return err
	}
	if !whole {
		return nil
	}
	for {
		k, err := us.next()
		if err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if k == xmltext.StartElement {
			return &SemanticError{action: "unmarshal", ByteOffset: src.Offset(), Err: errors.New("unexpected element after the top-level element")}
		}
	}
}

// addressableValue is a reflect.Value that is guaranteed to be addressable
// such that calling the Addr and Set methods do not panic.
//
// There is no compile magic that enforces this property,
// but rather the need to construct this type makes it easier to examine each
// construction site to ensure that this property is upheld.
type addressableValue struct {
	reflect.Value

	// forcedAddr reports whether this value is addressable
	// only through the use of [newAddressableValue].
	// This is only used for [jsonflags.CallMethodsWithLegacySemantics].
	forcedAddr bool
}

// newAddressableValue constructs a new addressable value of type t.
func newAddressableValue(t reflect.Type) addressableValue {
	return addressableValue{reflect.New(t).Elem(), true}
}

// arshaler holds the functions that marshal and unmarshal one Go type.
type arshaler struct {
	// marshal writes zero or more complete elements named name: none for
	// a nil pointer, one per item for a slice.
	marshal func(*marshalState, xmltext.Name, addressableValue) error
	// content writes the attributes and content of an element whose start
	// tag is open, nil if the type is not marshaled as a single element.
	content func(*marshalState, addressableValue) error
	// unmarshal reads the content of the element started by start,
	// through its end tag.
	unmarshal func(*unmarshalState, addressableValue) error
	// elemName returns the element name of a value given the name its
	// context chose (empty for a top-level value); nil to use that name,
	// or else the type name.
	elemName func(name xmltext.Name, va addressableValue) (xmltext.Name, error)

	// appendText and parseText handle the text form, used for attribute
	// values and character data; nil if the type has none.
	appendText func(*marshalState, []byte, addressableValue) ([]byte, error)
	parseText  func(*unmarshalState, []byte, addressableValue) error

	nonDefault bool
}

var lookupArshalerCache sync.Map // map[reflect.Type]*arshaler

func lookupArshaler(t reflect.Type) *arshaler {
	if v, ok := lookupArshalerCache.Load(t); ok {
		return v.(*arshaler)
	}
	fncs := makeDefaultArshaler(t)
	fncs = makeMethodArshaler(fncs, t)
	// Use the last stored so that duplicate arshalers can be garbage collected.
	v, _ := lookupArshalerCache.LoadOrStore(t, fncs)
	return v.(*arshaler)
}

// marshalValue writes va as element(s) named name, giving the functions of
// WithMarshalers precedence over the type's own.
func (ms *marshalState) marshalValue(name xmltext.Name, va addressableValue) error {
	return ms.marshalWith(name, va, lookupArshaler(va.Type()))
}

// marshalWith is marshalValue with a, the arshaler of va's type, already
// looked up, as a struct field or the item of a slice has it.
func (ms *marshalState) marshalWith(name xmltext.Name, va addressableValue, a *arshaler) error {
	if ms.marshalers != nil {
		if f := ms.marshalers.lookup(va.Type()); f != nil {
			return f.marshal(ms, name, va, a)
		}
	}
	return a.marshal(ms, name, va)
}

// appendTextValue appends the text form of va.
func (ms *marshalState) appendTextValue(dst []byte, va addressableValue) ([]byte, error) {
	return ms.appendTextWith(dst, va, lookupArshaler(va.Type()))
}

// appendTextWith is appendTextValue with the arshaler of va's type.
func (ms *marshalState) appendTextWith(dst []byte, va addressableValue, a *arshaler) ([]byte, error) {
	t := va.Type()
	if ms.marshalers != nil {
		if f := ms.marshalers.lookup(t); f != nil && f.text != nil {
			b, err := f.text(ms, dst, va)
			if err != SkipFunc {
				return b, err
			}
		}
	}
	if a.appendText == nil {
		return dst, newMarshalError(ms, xmltext.Name{}, t, errNotText)
	}
	return a.appendText(ms, dst, va)
}

// unmarshalValue reads the element started by start into va.
func (us *unmarshalState) unmarshalValue(va addressableValue) error {
	return us.unmarshalWith(va, lookupArshaler(va.Type()))
}

// unmarshalWith is unmarshalValue with the arshaler of va's type.
func (us *unmarshalState) unmarshalWith(va addressableValue, a *arshaler) error {
	if us.unmarshalers != nil {
		if f := us.unmarshalers.lookup(va.Type()); f != nil {
			return f.unmarshal(us, va, a)
		}
	}
	return a.unmarshal(us, va)
}

// parseTextValue sets va from its text form b.
func (us *unmarshalState) parseTextValue(b []byte, va addressableValue) error {
	return us.parseTextWith(b, va, lookupArshaler(va.Type()))
}

// parseTextWith is parseTextValue with the arshaler of va's type.
func (us *unmarshalState) parseTextWith(b []byte, va addressableValue, a *arshaler) error {
	t := va.Type()
	if us.unmarshalers != nil {
		if f := us.unmarshalers.lookup(t); f != nil && f.text != nil {
			if err := f.text(us, b, va); err != SkipFunc {
				return err
			}
		}
	}
	if a.parseText == nil {
		return newUnmarshalError(us, xmltext.Name{}, t, errNotText)
	}
	return a.parseText(us, b, va)
}

// readText reads the content of the element started by the last token
// through its end tag and returns its character data, in a buffer from
// takeBuf. Child elements are skipped.
func (us *unmarshalState) readText() ([]byte, error) {
	b := us.takeBuf()
	depth := 0
	for {
		k, err := us.next()
		if err != nil {
			us.putBuf(b)
			return nil, err
		}
		switch k {
		case xmltext.StartElement:
			depth++
		case xmltext.EndElement:
			if depth == 0 {
				return b, nil
			}
			depth--
		case xmltext.Text, xmltext.CData:
			if depth == 0 {
				b = append(b, us.text()...)
			}
		}
	}
}

// skipContent reads the content of the element started by the last token
// through its end tag.
func (us *unmarshalState) skipContent() error {
	depth := 0
	for {
		k, err := us.next()
		if err != nil {
			return err
		}
		switch k {
		case xmltext.StartElement:
			depth++
		case xmltext.EndElement:
			if depth == 0 {
				return nil
			}
			depth--
		}
	}
}

func unsafeBytes(s string) []byte {
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// xmltextSource is the TokenSource of an xmltext.Decoder.
type xmltextSource struct {
	dec   *xmltext.Decoder
	tok   xmltext.Token
	it    xmltext.AttrIter
	attrs bool // it is set for tok
}

func (s *xmltextSource) Next() (xmltext.Kind, error) {
	t, err := s.dec.ReadToken()
	s.tok, s.attrs = t, false
	return t.Kind(), err
}

func (s *xmltextSource) Name() xmltext.Name { return s.tok.Name() }

func (s *xmltextSource) NextAttr() (xmltext.Name, []byte, bool) {
	if !s.attrs {
		s.it, s.attrs = s.tok.Attrs(), true
	}
	if !s.it.Next() {
		return xmltext.Name{}, nil, false
	}
	return s.it.Name(), s.it.Value(), true
}

func (s *xmltextSource) Text() []byte                 { return s.tok.Text() }
func (s *xmltextSource) Offset() int64                { return s.dec.InputOffset() }
func (s *xmltextSource) PeekKind() xmltext.Kind       { return s.dec.PeekKind() }
func (s *xmltextSource) Depth() int                   { return export.OpenDepth(s.dec) }
func (s *xmltextSource) StableAttrs() bool            { return false }
func (s *xmltextSource) Capturing() bool              { return export.Capturing(s.dec) }
func (s *xmltextSource) StartCapture()                { s.dec.StartCapture() }
func (s *xmltextSource) CaptureLen() int              { return s.dec.CaptureLen() }
func (s *xmltextSource) Captured(from, to int) []byte { return s.dec.Captured(from, to) }
func (s *xmltextSource) StopCapture()                 { s.dec.StopCapture() }

// next, name, nextAttr and text read from src, calling an xmltext source
// directly: its methods are then not dispatched through the interface.
func (us *unmarshalState) next() (xmltext.Kind, error) {
	if us.dec != nil {
		t, err := us.dec.ReadToken()
		us.tsrc.tok, us.tsrc.attrs = t, false
		return t.Kind(), err
	}
	return us.src.Next()
}

func (us *unmarshalState) name() xmltext.Name {
	if us.dec != nil {
		return us.tsrc.Name()
	}
	return us.src.Name()
}

func (us *unmarshalState) nextAttr() (xmltext.Name, []byte, bool) {
	if us.dec != nil {
		return us.tsrc.NextAttr()
	}
	return us.src.NextAttr()
}

func (us *unmarshalState) text() []byte {
	if us.dec != nil {
		return us.tsrc.Text()
	}
	return us.src.Text()
}
