// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/Clickin/go-experiment-xml/xmltext"
)

// Marshalers is a list of functions that may override the marshal
// behavior of specific types. Populate it with [MarshalFunc],
// [MarshalToFunc] or [JoinMarshalers], and pass it with [WithMarshalers].
// A nil *Marshalers is equivalent to an empty list. Functions earlier in
// the list take precedence.
type Marshalers struct {
	fncs  []*typedMarshaler
	cache sync.Map // map[reflect.Type]*typedMarshaler, nil if none
}

// Unmarshalers is the unmarshal counterpart of [Marshalers].
type Unmarshalers struct {
	fncs  []*typedUnmarshaler
	cache sync.Map // map[reflect.Type]*typedUnmarshaler, nil if none
}

// JoinMarshalers constructs a flattened list of marshal functions.
func JoinMarshalers(ms ...*Marshalers) *Marshalers {
	out := new(Marshalers)
	for _, m := range ms {
		if m != nil {
			out.fncs = append(out.fncs, m.fncs...)
		}
	}
	return out
}

// JoinUnmarshalers constructs a flattened list of unmarshal functions.
func JoinUnmarshalers(us ...*Unmarshalers) *Unmarshalers {
	out := new(Unmarshalers)
	for _, u := range us {
		if u != nil {
			out.fncs = append(out.fncs, u.fncs...)
		}
	}
	return out
}

type typedMarshaler struct {
	typ reflect.Type // the T of the function
	// One of text and content is set.
	text    func(*marshalState, []byte, addressableValue) ([]byte, error)
	content func(*marshalState, addressableValue) error
}

type typedUnmarshaler struct {
	typ reflect.Type // the T of the function
	// One of text and content is set.
	text    func(*unmarshalState, []byte, addressableValue) error
	content func(*unmarshalState, addressableValue) error
}

func (m *Marshalers) lookup(t reflect.Type) *typedMarshaler {
	if v, ok := m.cache.Load(t); ok {
		return v.(*typedMarshaler)
	}
	var found *typedMarshaler
	for _, f := range m.fncs {
		if castableTo(t, f.typ) {
			found = f
			break
		}
	}
	m.cache.Store(t, found)
	return found
}

func (u *Unmarshalers) lookup(t reflect.Type) *typedUnmarshaler {
	if v, ok := u.cache.Load(t); ok {
		return v.(*typedUnmarshaler)
	}
	var found *typedUnmarshaler
	for _, f := range u.fncs {
		if castableTo(t, f.typ) {
			found = f
			break
		}
	}
	u.cache.Store(t, found)
	return found
}

// marshal writes va as an element named name with f, falling back to a
// if f returns SkipFunc.
func (f *typedMarshaler) marshal(ms *marshalState, name xmltext.Name, va addressableValue, a *arshaler) error {
	t := va.Type()
	if f.text != nil {
		b, err := f.text(ms, ms.buf[:0], va)
		if err == SkipFunc {
			return a.marshal(ms, name, va)
		}
		ms.buf = b[:0]
		if err != nil {
			return newMarshalError(ms, name, t, err)
		}
		if name, err = a.nameFor(name, va); err != nil {
			return newMarshalError(ms, name, t, err)
		}
		name = ms.resolve(name)
		enc := ms.enc
		if err := enc.BeginElement(name); err != nil {
			return err
		}
		if err := writeText(enc, b); err != nil {
			return err
		}
		return enc.EndElement()
	}
	name, err := a.nameFor(name, va)
	if err != nil {
		return newMarshalError(ms, name, t, err)
	}
	name = ms.resolve(name)
	enc := ms.enc
	if err := enc.BeginElement(name); err != nil {
		return err
	}
	prevSpace := ms.elemSpace
	defer func() { ms.elemSpace = prevSpace }()
	ms.elemSpace = name.Space
	depth, off := enc.StackDepth(), enc.OutputOffset()+int64(enc.Buffered())
	err = f.content(ms, va)
	if err == SkipFunc {
		if a.content == nil || enc.StackDepth() != depth || enc.OutputOffset()+int64(enc.Buffered()) != off {
			return newMarshalError(ms, name, t, errSkipMisplace)
		}
		ms.elemSpace = name.Space
		err = a.content(ms, va)
	}
	if err != nil {
		return newMarshalError(ms, name, t, err)
	}
	if enc.StackDepth() != depth {
		return newMarshalError(ms, name, t, errUnbalanced)
	}
	return enc.EndElement()
}

// unmarshal reads the element started by start into va with f, falling
// back to a if f returns SkipFunc.
func (f *typedUnmarshaler) unmarshal(us *unmarshalState, va addressableValue, a *arshaler) error {
	t := va.Type()
	if f.text != nil {
		b, err := us.readText()
		if err != nil {
			return err
		}
		err = f.text(us, b, va)
		if err == SkipFunc {
			if a.parseText == nil {
				err = errSkipMisplace
			} else {
				err = a.parseText(us, b, va)
			}
		}
		us.putBuf(b)
		if err != nil {
			return newUnmarshalError(us, xmltext.Name{}, t, err)
		}
		return nil
	}
	src := us.src
	depth, off := src.Depth(), src.Offset()
	err := f.content(us, va)
	if err == SkipFunc {
		if src.Offset() != off {
			return newUnmarshalError(us, xmltext.Name{}, t, errSkipMisplace)
		}
		return a.unmarshal(us, va)
	}
	if err != nil {
		return newUnmarshalError(us, xmltext.Name{}, t, err)
	}
	if src.Depth() != depth-1 {
		return newUnmarshalError(us, xmltext.Name{}, t, errNotConsumed)
	}
	return nil
}

// castableTo reports whether values of type from can be cast to type to,
// as in encoding/json/v2: a function for T applies to values of type T, a
// function for *T or for an interface *T implements to addressable values
// of type T.
func castableTo(from, to reflect.Type) bool {
	switch to.Kind() {
	case reflect.Interface:
		return reflect.PointerTo(from).Implements(to)
	case reflect.Pointer:
		return reflect.PointerTo(from) == to
	default:
		return from == to
	}
}

// castTo casts va to T; castableTo(va.Type(), T) must hold.
func castTo[T any](va addressableValue, to reflect.Type) T {
	v := va.Value
	if to.Kind() == reflect.Interface || to.Kind() == reflect.Pointer {
		v = va.Addr()
	}
	x, _ := reflect.TypeAssert[T](v)
	return x
}

func funcType[T any](unmarshal bool) reflect.Type {
	t := reflect.TypeFor[T]()
	if t.Kind() == reflect.Pointer && t.Name() != "" {
		panic(fmt.Sprintf("xml: invalid function type %v: named pointer", t))
	}
	if unmarshal && t.Kind() != reflect.Pointer && t.Kind() != reflect.Interface {
		panic(fmt.Sprintf("xml: invalid unmarshal function type %v: must be a pointer or interface", t))
	}
	return t
}

// MarshalFunc constructs a type-specific marshaler that specifies how to
// marshal values of type T as text: the content of a simple element or an
// attribute value. T can be any type except a named pointer. The function
// may return [SkipFunc] to fall back to the type's own representation.
func MarshalFunc[T any](fn func(T) ([]byte, error)) *Marshalers {
	typ := funcType[T](false)
	return &Marshalers{fncs: []*typedMarshaler{{
		typ: typ,
		text: func(_ *marshalState, dst []byte, va addressableValue) ([]byte, error) {
			b, err := fn(castTo[T](va, typ))
			if err != nil {
				return dst, err
			}
			return append(dst, b...), nil
		},
	}}}
}

// MarshalToFunc constructs a type-specific marshaler that writes the
// attributes and content of an element for values of type T, with the
// requirements of [MarshalerTo]. It is not used for attribute values.
func MarshalToFunc[T any](fn func(*xmltext.Encoder, T) error) *Marshalers {
	typ := funcType[T](false)
	return &Marshalers{fncs: []*typedMarshaler{{
		typ: typ,
		content: func(ms *marshalState, va addressableValue) error {
			return fn(ms.enc, castTo[T](va, typ))
		},
	}}}
}

// UnmarshalFunc constructs a type-specific unmarshaler that specifies how
// to unmarshal values of type T from text: the character data of a simple
// element or an attribute value. T must be a pointer or interface type.
// The text is valid only during the call.
func UnmarshalFunc[T any](fn func([]byte, T) error) *Unmarshalers {
	typ := funcType[T](true)
	return &Unmarshalers{fncs: []*typedUnmarshaler{{
		typ: typ,
		text: func(_ *unmarshalState, b []byte, va addressableValue) error {
			return fn(b, castTo[T](va, typ))
		},
	}}}
}

// UnmarshalFromFunc constructs a type-specific unmarshaler that reads an
// element into values of type T, with the requirements of
// [UnmarshalerFrom]. T must be a pointer or interface type. It is not used
// for attribute values.
func UnmarshalFromFunc[T any](fn func(*xmltext.Decoder, xmltext.Token, T) error) *Unmarshalers {
	typ := funcType[T](true)
	return &Unmarshalers{fncs: []*typedUnmarshaler{{
		typ: typ,
		content: func(us *unmarshalState, va addressableValue) error {
			if us.dec == nil {
				return errNoDecoder
			}
			return fn(us.dec, us.tsrc.tok, castTo[T](va, typ))
		},
	}}}
}
