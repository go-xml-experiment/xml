// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"encoding"
	"reflect"

	"github.com/Clickin/go-experiment-xml/xmltext"
)

var (
	marshalerToType     = reflect.TypeFor[MarshalerTo]()
	unmarshalerFromType = reflect.TypeFor[UnmarshalerFrom]()
	textAppenderType    = reflect.TypeFor[encoding.TextAppender]()
	textMarshalerType   = reflect.TypeFor[encoding.TextMarshaler]()
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// MarshalerTo is implemented by types that marshal themselves as an
// element. MarshalXMLTo is called with the element's start tag open: it may
// add attributes with [xmltext.Encoder.Attr], then write the content, and
// must leave the element open, with any elements it began ended. The
// caller writes the end tag. Its element name is chosen by the caller.
//
// The method may call [MarshalEncode] to marshal a child element with the
// options of the current call. It must not retain the Encoder after it
// returns: the Encoder may be pooled and reused.
type MarshalerTo interface {
	MarshalXMLTo(*xmltext.Encoder) error
}

// UnmarshalerFrom is implemented by types that unmarshal themselves from
// an element. UnmarshalXMLFrom is called after the start tag start was
// read: start is valid until the next call on the Decoder, so its name and
// attributes must be read first. The method must read the content of the
// element through its end tag.
//
// The method may call [UnmarshalDecode] to unmarshal a child element with
// the options of the current call. It must not retain the Decoder or start
// after it returns: the Decoder may be pooled and reused.
type UnmarshalerFrom interface {
	UnmarshalXMLFrom(*xmltext.Decoder, xmltext.Token) error
}

// makeMethodArshaler overrides fncs with the methods t has, in order of
// precedence MarshalerTo, TextAppender, TextMarshaler for marshaling and
// UnmarshalerFrom, TextUnmarshaler for unmarshaling.
//
// An unnamed pointer type gets its methods from its element type, which
// calls methods on pointer receivers through the address; nil pointers
// are thus handled before any method is called.
func makeMethodArshaler(fncs *arshaler, t reflect.Type) *arshaler {
	switch {
	case t.Kind() == reflect.Interface, t.Kind() == reflect.Pointer && t.Name() == "":
		return fncs
	case t.NumMethod() == 0 && reflect.PointerTo(t).NumMethod() == 0:
		return fncs
	}

	if needAddr, ok := implements(t, textAppenderType); ok {
		fncs.nonDefault = true
		fncs.appendText = func(_ *marshalState, b []byte, va addressableValue) ([]byte, error) {
			m, _ := reflect.TypeAssert[encoding.TextAppender](recv(va, needAddr))
			return m.AppendText(b)
		}
		textContent(fncs, t)
	} else if needAddr, ok := implements(t, textMarshalerType); ok {
		fncs.nonDefault = true
		fncs.appendText = func(_ *marshalState, b []byte, va addressableValue) ([]byte, error) {
			m, _ := reflect.TypeAssert[encoding.TextMarshaler](recv(va, needAddr))
			s, err := m.MarshalText()
			return append(b, s...), err
		}
		textContent(fncs, t)
	}
	if needAddr, ok := implements(t, textUnmarshalerType); ok {
		fncs.nonDefault = true
		fncs.parseText = func(_ *unmarshalState, b []byte, va addressableValue) error {
			m, _ := reflect.TypeAssert[encoding.TextUnmarshaler](recv(va, needAddr))
			return m.UnmarshalText(b)
		}
		parseText := fncs.parseText
		fncs.unmarshal = func(us *unmarshalState, va addressableValue) error {
			b, err := us.readText()
			if err != nil {
				return err
			}
			err = parseText(us, b, va)
			us.putBuf(b)
			if err != nil {
				return newUnmarshalError(us, xmltext.Name{}, t, err)
			}
			return nil
		}
	}

	if needAddr, ok := implements(t, marshalerToType); ok {
		fncs.nonDefault = true
		fncs.content = func(ms *marshalState, va addressableValue) error {
			m, _ := reflect.TypeAssert[MarshalerTo](recv(va, needAddr))
			depth := ms.enc.StackDepth()
			if err := m.MarshalXMLTo(ms.enc); err != nil {
				return newMarshalError(ms, xmltext.Name{}, t, err)
			}
			if ms.enc.StackDepth() != depth {
				return newMarshalError(ms, xmltext.Name{}, t, errUnbalanced)
			}
			return nil
		}
		wrapElement(fncs, t)
	}
	if needAddr, ok := implements(t, unmarshalerFromType); ok {
		fncs.nonDefault = true
		fncs.unmarshal = func(us *unmarshalState, va addressableValue) error {
			m, _ := reflect.TypeAssert[UnmarshalerFrom](recv(va, needAddr))
			if us.dec == nil {
				return newUnmarshalError(us, xmltext.Name{}, t, errNoDecoder)
			}
			depth := us.src.Depth()
			if err := m.UnmarshalXMLFrom(us.dec, us.tsrc.tok); err != nil {
				return newUnmarshalError(us, xmltext.Name{}, t, err)
			}
			if us.src.Depth() != depth-1 {
				return newUnmarshalError(us, xmltext.Name{}, t, errNotConsumed)
			}
			return nil
		}
	}
	return fncs
}

// textContent makes the element form of fncs its text form.
func textContent(fncs *arshaler, t reflect.Type) {
	appendText := fncs.appendText
	fncs.content = func(ms *marshalState, va addressableValue) error {
		b, err := appendText(ms, ms.buf[:0], va)
		ms.buf = b[:0]
		if err != nil {
			return newMarshalError(ms, xmltext.Name{}, t, err)
		}
		return writeText(ms.enc, b)
	}
	wrapElement(fncs, t)
}

// recv returns the receiver for a method of va's type, or of its pointer
// type if needAddr.
func recv(va addressableValue, needAddr bool) reflect.Value {
	if needAddr {
		return va.Addr()
	}
	return va.Value
}

// implements reports whether t or a pointer to it implements ifaceType,
// and if only the pointer does.
func implements(t, ifaceType reflect.Type) (needAddr, ok bool) {
	switch {
	case t.Implements(ifaceType):
		return false, true
	case reflect.PointerTo(t).Implements(ifaceType):
		return true, true
	}
	return false, false
}
