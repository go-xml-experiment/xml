// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"errors"
	"reflect"
	"strings"

	"github.com/Clickin/go-experiment-xml/xmltext"
)

// ErrUnknownName is the error of a [SemanticError] for an attribute or
// element that no struct field takes, when [RejectUnknownNames] is set.
var ErrUnknownName = errors.New("unknown name")

// SkipFunc may be returned by a function registered with [MarshalToFunc]
// or [UnmarshalFromFunc], before it writes or reads anything, to fall back
// to the next representation of the type.
var SkipFunc = errors.New("xml: skip function")

var (
	errNotText      = errors.New("type has no text representation")
	errUnsupported  = errors.New("unsupported type")
	errUnbalanced   = errors.New("must leave the element it is given open and balanced")
	errNotConsumed  = errors.New("must read through the end of the element it is given")
	errSkipMisplace = errors.New("SkipFunc returned after writing or reading")
	errNoDecoder    = errors.New("no xmltext.Decoder to read from")
)

// SemanticError describes a failure to marshal or unmarshal a Go value
// that is not a syntax error of the XML itself.
type SemanticError struct {
	action string // "marshal" or "unmarshal"

	// ByteOffset is the input or output offset where the error occurred.
	ByteOffset int64
	// Name is the name of the element or attribute being processed, if any.
	Name xmltext.Name
	// GoType is the Go type that could not be handled.
	GoType reflect.Type
	// Err is the underlying error.
	Err error
}

func (e *SemanticError) Error() string {
	var b strings.Builder
	b.WriteString("xml: cannot ")
	if e.action == "" {
		b.WriteString("handle")
	} else {
		b.WriteString(e.action)
	}
	if e.GoType != nil {
		b.WriteString(" Go type ")
		b.WriteString(e.GoType.String())
	}
	if e.Name.Local != "" {
		b.WriteString(" for ")
		if e.Name.Space != "" {
			b.WriteString("{" + e.Name.Space + "}")
		}
		b.WriteString(e.Name.Local)
	}
	if e.Err != nil {
		b.WriteString(": ")
		b.WriteString(e.Err.Error())
	}
	return b.String()
}

func (e *SemanticError) Unwrap() error { return e.Err }

func newMarshalError(ms *marshalState, name xmltext.Name, t reflect.Type, err error) error {
	var se *SemanticError
	if errors.As(err, &se) || isSyntactic(err) {
		return err
	}
	return &SemanticError{action: "marshal", ByteOffset: ms.enc.OutputOffset(), Name: name, GoType: t, Err: err}
}

func newUnmarshalError(us *unmarshalState, name xmltext.Name, t reflect.Type, err error) error {
	var se *SemanticError
	if errors.As(err, &se) || isSyntactic(err) {
		return err
	}
	return &SemanticError{action: "unmarshal", ByteOffset: us.src.Offset(), Name: name, GoType: t, Err: err}
}

func isSyntactic(err error) bool {
	var se *xmltext.SyntaxError
	return errors.As(err, &se)
}
