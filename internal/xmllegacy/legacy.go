// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package xmllegacy connects the v1 package to the v2 package, which
// implements encoding/xml's marshaling with the semantics of version 1.
// encoding/xml supplies its method interfaces and types through V1;
// the v2 package supplies the entry points.
package xmllegacy

import (
	"reflect"

	"github.com/Clickin/go-experiment-xml/xmltext"
)

// Start is a start tag chosen by the caller: the start argument of
// encoding/xml's Encoder.EncodeElement.
type Start struct {
	Name xmltext.Name
	Attr []xmltext.Attr
}

// Hooks are the parts of the v1 package that the v2 package needs.
type Hooks struct {
	MarshalerType     reflect.Type // encoding/xml.Marshaler
	MarshalerAttrType reflect.Type // encoding/xml.MarshalerAttr
	NameType          reflect.Type // encoding/xml.Name
	AttrType          reflect.Type // encoding/xml.Attr

	UnmarshalerType     reflect.Type // encoding/xml.Unmarshaler
	UnmarshalerAttrType reflect.Type // encoding/xml.UnmarshalerAttr

	// CallMarshaler calls the MarshalXML method of recv with the Encoder
	// owner, which writes to the same xmltext.Encoder.
	CallMarshaler func(owner any, recv reflect.Value, start Start) error
	// CallMarshalerAttr calls the MarshalXMLAttr method of recv.
	CallMarshalerAttr func(recv reflect.Value, name xmltext.Name) (xmltext.Attr, error)
	// ParentSpace returns the namespace of the innermost element owner
	// opened itself (with EncodeToken), "" if none.
	ParentSpace func(owner any) string
	// UnsupportedType returns encoding/xml's UnsupportedTypeError.
	UnsupportedType func(t reflect.Type) error
	// TagPathError returns encoding/xml's TagPathError.
	TagPathError func(t reflect.Type, field1, tag1, field2, tag2 string) error

	// UnmarshalXML calls the UnmarshalXML method of v, or else of its
	// address, if it has one, as encoding/xml looks for it: with the
	// Decoder of owner, the TokenSource being read, and its current start
	// element. It reports whether there was a method.
	UnmarshalXML func(owner TokenSource, v reflect.Value) (bool, error)
	// UnmarshalXMLAttr is UnmarshalXML for the UnmarshalXMLAttr method.
	UnmarshalXMLAttr func(v reflect.Value, a xmltext.Attr) (bool, error)
	// UnmarshalError returns encoding/xml's UnmarshalError.
	UnmarshalError func(msg string) error
	// ErrUnmarshalDepth is the error for input nested too deeply.
	ErrUnmarshalDepth error
}

// V1 is set by encoding/xml.
var V1 *Hooks

// Marshal is set by the v2 package. It writes v to enc with the
// semantics of encoding/xml's Encoder.Encode (start nil) or EncodeElement.
// owner is the encoding/xml Encoder, passed back to the hooks.
var Marshal func(enc *xmltext.Encoder, owner any, v reflect.Value, start *Start) error

// Field is a struct field as encoding/xml sees it, from StructFields.
type Field struct {
	Index   []int
	Name    xmltext.Name // Space is the namespace from the tag
	Parents []string
	Flags   uint8 // of F*
}

// Field flags.
const (
	FElement uint8 = 1 << iota
	FAttr
	FCDATA
	FCharData
	FInnerXML
	FComment
	FAny
	FOmitEmpty
)

// StructFields is set by the v2 package. It returns the XMLName field and
// the other fields of struct type t under the rules of encoding/xml, which
// the v2 package also marshals with.
var StructFields func(t reflect.Type) (xmlname *Field, fields []Field, err error)

// TokenSource is what the v2 package reads an element from when it
// unmarshals: an xmltext.Decoder, or an encoding/xml Decoder with its own
// nesting and name space rules. Names are namespace-resolved.
type TokenSource interface {
	// Next advances to the next token and returns its kind.
	Next() (xmltext.Kind, error)
	// Name returns the name of the current start or end element.
	Name() xmltext.Name
	// NextAttr returns the next attribute of the current start element.
	// If StableAttrs, its value is immutable and may be kept.
	NextAttr() (name xmltext.Name, value []byte, ok bool)
	StableAttrs() bool
	// Text returns the content of the current text, CDATA or comment
	// token, valid until Next.
	Text() []byte
	// Offset returns the input offset after the current token.
	Offset() int64
	// PeekKind returns the kind of the next token without reading it.
	PeekKind() xmltext.Kind
	// Depth returns the number of open elements.
	Depth() int

	// The capture of raw input for innerxml, as in xmltext.Decoder.
	// Capturing reports whether a capture is active.
	Capturing() bool
	StartCapture()
	CaptureLen() int
	Captured(from, to int) []byte
	StopCapture()
}

// Unmarshal is set by the v2 package. It reads an element from src into
// v, a non-nil pointer, with the semantics of encoding/xml's
// Decoder.DecodeElement: if started, the current token of src is the start
// element; otherwise tokens up to the next start element are skipped.
var Unmarshal func(src TokenSource, v reflect.Value, started bool) error
