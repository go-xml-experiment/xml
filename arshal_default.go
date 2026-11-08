// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"sync"

	"github.com/Clickin/go-experiment-xml/xmltext"
)

var (
	stringType = reflect.TypeFor[string]()
	bytesType  = reflect.TypeFor[[]byte]()
	nameType   = reflect.TypeFor[xmltext.Name]()
	attrType   = reflect.TypeFor[xmltext.Attr]()
)

func makeDefaultArshaler(t reflect.Type) *arshaler {
	switch t.Kind() {
	case reflect.Bool:
		return makeTextArshaler(t, appendBool, parseBool)
	case reflect.String:
		return makeStringArshaler(t)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return makeTextArshaler(t, appendInt, parseInt)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return makeTextArshaler(t, appendUint, parseUint)
	case reflect.Float32, reflect.Float64:
		return makeTextArshaler(t, appendFloat, parseFloat)
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return makeTextArshaler(t, appendBytes, parseBytes)
		}
		return makeSliceArshaler(t)
	case reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return makeTextArshaler(t, appendBytes, parseByteArray)
		}
		return makeArrayArshaler(t)
	case reflect.Pointer:
		return makePointerArshaler(t)
	case reflect.Interface:
		return makeInterfaceArshaler(t)
	case reflect.Struct:
		return makeStructArshaler(t)
	}
	return makeUnsupportedArshaler(t)
}

// elementName returns name, or if it has no local part, the name of t.
func elementName(name xmltext.Name, t reflect.Type) (xmltext.Name, error) {
	if name.Local == "" {
		name.Local = t.Name()
		if name.Local == "" {
			return name, errors.New("unnamed type needs an element name")
		}
	}
	return name, nil
}

// writeText writes b as character data. Empty text writes nothing, so that
// an element without content is written as an empty-element tag.
func writeText(enc *xmltext.Encoder, b []byte) error {
	if len(b) == 0 {
		return nil
	}
	return enc.TextBytes(b)
}

// nameFor returns the element name of va given the name its context chose.
func (a *arshaler) nameFor(name xmltext.Name, va addressableValue) (xmltext.Name, error) {
	if a.elemName != nil {
		return a.elemName(name, va)
	}
	return elementName(name, va.Type())
}

// wrapElement sets a.marshal to write a single element with a.content as
// its content.
func wrapElement(a *arshaler, t reflect.Type) {
	content, nameOf := a.content, a.elemName
	a.marshal = func(ms *marshalState, name xmltext.Name, va addressableValue) error {
		var err error
		if nameOf != nil {
			name, err = nameOf(name, va)
		} else {
			name, err = elementName(name, t)
		}
		if err != nil {
			return newMarshalError(ms, name, t, err)
		}
		name = ms.resolve(name)
		enc := ms.enc
		if err := enc.BeginElement(name); err != nil {
			return err
		}
		prev := ms.elemSpace
		ms.elemSpace = name.Space
		err = content(ms, va)
		ms.elemSpace = prev
		if err != nil {
			return err
		}
		return enc.EndElement()
	}
}

// makeTextArshaler returns the arshaler of a type whose element form is its
// text form as character data.
func makeTextArshaler(t reflect.Type,
	appendText func(*marshalState, []byte, addressableValue) ([]byte, error),
	parseText func(*unmarshalState, []byte, addressableValue) error) *arshaler {
	a := &arshaler{appendText: appendText, parseText: parseText}
	a.content = func(ms *marshalState, va addressableValue) error {
		b, err := appendText(ms, ms.buf[:0], va)
		ms.buf = b[:0]
		if err != nil {
			return newMarshalError(ms, xmltext.Name{}, t, err)
		}
		return writeText(ms.enc, b)
	}
	wrapElement(a, t)
	a.unmarshal = func(us *unmarshalState, va addressableValue) error {
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
	return a
}

func makeStringArshaler(t reflect.Type) *arshaler {
	a := makeTextArshaler(t,
		func(_ *marshalState, b []byte, va addressableValue) ([]byte, error) {
			return append(b, va.String()...), nil
		},
		func(_ *unmarshalState, b []byte, va addressableValue) error {
			va.SetString(string(b))
			return nil
		})
	a.content = func(ms *marshalState, va addressableValue) error {
		if va.Len() == 0 {
			return nil
		}
		return ms.enc.Text(va.String())
	}
	wrapElement(a, t)
	return a
}

func appendBool(_ *marshalState, b []byte, va addressableValue) ([]byte, error) {
	return strconv.AppendBool(b, va.Bool()), nil
}

func parseBool(_ *unmarshalState, b []byte, va addressableValue) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		va.SetBool(false)
		return nil
	}
	v, err := strconv.ParseBool(string(b))
	if err != nil {
		return err
	}
	va.SetBool(v)
	return nil
}

func appendInt(_ *marshalState, b []byte, va addressableValue) ([]byte, error) {
	return strconv.AppendInt(b, va.Int(), 10), nil
}

func parseInt(_ *unmarshalState, b []byte, va addressableValue) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		va.SetInt(0)
		return nil
	}
	v, err := strconv.ParseInt(string(b), 10, va.Type().Bits())
	if err != nil {
		return err
	}
	va.SetInt(v)
	return nil
}

func appendUint(_ *marshalState, b []byte, va addressableValue) ([]byte, error) {
	return strconv.AppendUint(b, va.Uint(), 10), nil
}

func parseUint(_ *unmarshalState, b []byte, va addressableValue) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		va.SetUint(0)
		return nil
	}
	v, err := strconv.ParseUint(string(b), 10, va.Type().Bits())
	if err != nil {
		return err
	}
	va.SetUint(v)
	return nil
}

func appendFloat(_ *marshalState, b []byte, va addressableValue) ([]byte, error) {
	return strconv.AppendFloat(b, va.Float(), 'g', -1, va.Type().Bits()), nil
}

func parseFloat(_ *unmarshalState, b []byte, va addressableValue) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 {
		va.SetFloat(0)
		return nil
	}
	v, err := strconv.ParseFloat(string(b), va.Type().Bits())
	if err != nil {
		return err
	}
	va.SetFloat(v)
	return nil
}

// Byte slices and arrays are text written verbatim, as in encoding/xml
// (encoding/json/v2 uses base64).
func appendBytes(_ *marshalState, b []byte, va addressableValue) ([]byte, error) {
	return append(b, va.Bytes()...), nil
}

func parseBytes(_ *unmarshalState, b []byte, va addressableValue) error {
	if va.IsNil() || va.Cap() < len(b) {
		va.SetBytes(append([]byte{}, b...))
		return nil
	}
	va.SetBytes(append(va.Bytes()[:0], b...))
	return nil
}

func parseByteArray(_ *unmarshalState, b []byte, va addressableValue) error {
	n := copy(va.Bytes(), b)
	clear(va.Bytes()[n:])
	return nil
}

func makeUnsupportedArshaler(t reflect.Type) *arshaler {
	return &arshaler{
		marshal: func(ms *marshalState, name xmltext.Name, _ addressableValue) error {
			return newMarshalError(ms, name, t, errUnsupported)
		},
		unmarshal: func(us *unmarshalState, _ addressableValue) error {
			return newUnmarshalError(us, us.src.Name(), t, errUnsupported)
		},
	}
}

// A slice is one element per item.
func makeSliceArshaler(t reflect.Type) *arshaler {
	et := t.Elem()
	var (
		once    sync.Once
		valFncs *arshaler
	)
	init := func() { valFncs = lookupArshaler(et) }
	return &arshaler{
		marshal: func(ms *marshalState, name xmltext.Name, va addressableValue) error {
			once.Do(init)
			for i, n := 0, va.Len(); i < n; i++ {
				if err := ms.marshalWith(name, addressableValue{va.Index(i), va.forcedAddr}, valFncs); err != nil {
					return err
				}
			}
			return nil
		},
		unmarshal: func(us *unmarshalState, va addressableValue) error {
			n := va.Len()
			va.Grow(1)
			va.SetLen(n + 1)
			va.Index(n).SetZero() // reused or grown capacity need not be zero
			once.Do(init)
			return us.unmarshalWith(addressableValue{va.Index(n), false}, valFncs)
		},
	}
}

// An array is one element per item when marshaled. It is not unmarshaled:
// unlike a slice, it has no length to find the next item by.
func makeArrayArshaler(t reflect.Type) *arshaler {
	a := makeUnsupportedArshaler(t)
	a.marshal = func(ms *marshalState, name xmltext.Name, va addressableValue) error {
		for i, n := 0, va.Len(); i < n; i++ {
			if err := ms.marshalValue(name, addressableValue{va.Index(i), va.forcedAddr}); err != nil {
				return err
			}
		}
		return nil
	}
	return a
}

// A nil pointer is omitted; unmarshaling allocates.
func makePointerArshaler(t reflect.Type) *arshaler {
	et := t.Elem()
	var (
		once    sync.Once
		valFncs *arshaler
	)
	init := func() { valFncs = lookupArshaler(et) }
	return &arshaler{
		marshal: func(ms *marshalState, name xmltext.Name, va addressableValue) error {
			if va.IsNil() {
				return nil
			}
			once.Do(init)
			return ms.marshalWith(name, addressableValue{va.Elem(), false}, valFncs)
		},
		unmarshal: func(us *unmarshalState, va addressableValue) error {
			if va.IsNil() {
				va.Set(reflect.New(et))
			}
			once.Do(init)
			return us.unmarshalWith(addressableValue{va.Elem(), false}, valFncs)
		},
		appendText: func(ms *marshalState, b []byte, va addressableValue) ([]byte, error) {
			if va.IsNil() {
				return b, nil
			}
			once.Do(init)
			return ms.appendTextWith(b, addressableValue{va.Elem(), false}, valFncs)
		},
		parseText: func(us *unmarshalState, b []byte, va addressableValue) error {
			if va.IsNil() {
				va.Set(reflect.New(et))
			}
			once.Do(init)
			return us.parseTextWith(b, addressableValue{va.Elem(), false}, valFncs)
		},
	}
}

// An interface holds the value marshaled; it is unmarshaled only into a
// non-nil pointer it holds.
func makeInterfaceArshaler(t reflect.Type) *arshaler {
	dynamic := func(va addressableValue) addressableValue {
		v := newAddressableValue(va.Elem().Type())
		v.Set(va.Elem())
		return v
	}
	held := func(va addressableValue) (addressableValue, bool) {
		if va.IsNil() || va.Elem().Kind() != reflect.Pointer || va.Elem().IsNil() {
			return addressableValue{}, false
		}
		return addressableValue{va.Elem().Elem(), false}, true
	}
	return &arshaler{
		marshal: func(ms *marshalState, name xmltext.Name, va addressableValue) error {
			if va.IsNil() {
				return nil
			}
			return ms.marshalValue(name, dynamic(va))
		},
		unmarshal: func(us *unmarshalState, va addressableValue) error {
			v, ok := held(va)
			if !ok {
				return newUnmarshalError(us, us.src.Name(), t, errors.New("cannot unmarshal into an interface without a pointer in it"))
			}
			return us.unmarshalValue(v)
		},
		appendText: func(ms *marshalState, b []byte, va addressableValue) ([]byte, error) {
			if va.IsNil() {
				return b, nil
			}
			return ms.appendTextValue(b, dynamic(va))
		},
		parseText: func(us *unmarshalState, b []byte, va addressableValue) error {
			v, ok := held(va)
			if !ok {
				return errors.New("cannot unmarshal into an interface without a pointer in it")
			}
			return us.parseTextValue(b, v)
		},
	}
}

// isNil reports whether va is a nil pointer or interface, which are
// omitted.
func isNil(va addressableValue) bool {
	switch va.Kind() {
	case reflect.Pointer, reflect.Interface:
		return va.IsNil()
	}
	return false
}

// isEmpty is the omitempty test of encoding/xml.
func isEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
		reflect.Float32, reflect.Float64, reflect.Interface, reflect.Pointer:
		return v.IsZero()
	}
	return false
}

type isZeroer interface{ IsZero() bool }

var isZeroerType = reflect.TypeFor[isZeroer]()

// isZero is the omitzero test of encoding/json/v2: an IsZero method if the
// type has one, the zero value otherwise.
func isZero(va addressableValue) bool {
	t := va.Type()
	switch {
	case t.Kind() == reflect.Interface && t.Implements(isZeroerType):
		return va.IsNil() || va.Interface().(isZeroer).IsZero()
	case t.Kind() == reflect.Pointer && t.Implements(isZeroerType):
		return va.IsNil() || va.Interface().(isZeroer).IsZero()
	case t.Implements(isZeroerType):
		return va.Interface().(isZeroer).IsZero()
	case reflect.PointerTo(t).Implements(isZeroerType):
		return va.Addr().Interface().(isZeroer).IsZero()
	}
	return va.IsZero()
}

// Names and attributes: XMLName fields and ",any,attr" fields hold
// xmltext.Name and xmltext.Attr values, or values of types with the same
// structure (such as encoding/xml's Name and Attr).

func isNameType(t reflect.Type) bool {
	return t == nameType || t.Kind() == reflect.Struct && t.ConvertibleTo(nameType)
}

func isAttrType(t reflect.Type) bool {
	if t == attrType {
		return true
	}
	if t.Kind() != reflect.Struct || t.NumField() != 2 {
		return false
	}
	n, v := t.Field(0), t.Field(1)
	return n.Name == "Name" && isNameType(n.Type) && v.Name == "Value" && v.Type.Kind() == reflect.String
}

func getName(v reflect.Value) xmltext.Name {
	return v.Convert(nameType).Interface().(xmltext.Name)
}

func setName(v reflect.Value, n xmltext.Name) {
	v.Set(reflect.ValueOf(n).Convert(v.Type()))
}

func getAttr(v reflect.Value) xmltext.Attr {
	return xmltext.Attr{Name: getName(v.Field(0)), Value: v.Field(1).String()}
}

func setAttr(v reflect.Value, a xmltext.Attr) {
	setName(v.Field(0), a.Name)
	v.Field(1).SetString(a.Value)
}

func isNSDecl(n xmltext.Name) bool {
	return n.Space == xmltext.XMLNSNamespace || n.Space == "" && n.Local == "xmlns"
}

// A struct is an element holding its fields.
func makeStructArshaler(t reflect.Type) *arshaler {
	var (
		once   sync.Once
		fields *structFields
		ferr   error
	)
	init := func() {
		fields, ferr = cachedStructFields(t, false)
		if ferr != nil {
			return
		}
		if xn := fields.xmlname; xn != nil && !isNameType(xn.typ) && xn.typ.Kind() != reflect.Struct {
			ferr = fmt.Errorf("xml: XMLName field of %s must be a name or struct type", t)
		}
	}
	a := &arshaler{}
	a.content = func(ms *marshalState, va addressableValue) error {
		if once.Do(init); ferr != nil {
			return ferr
		}
		return ms.marshalStruct(fields, va)
	}
	a.elemName = func(name xmltext.Name, va addressableValue) (xmltext.Name, error) {
		if once.Do(init); ferr != nil {
			return name, ferr
		}
		// The element name: the XMLName tag, else a set XMLName value,
		// else the name given, else the type name; as in encoding/xml.
		if xn := fields.xmlname; xn != nil {
			if xn.name.Local != "" {
				name = xn.name
			} else if fv, ok := fieldValue(va, xn.index, false); ok && isNameType(xn.typ) {
				if n := getName(fv.Value); n.Local != "" {
					name = n
				}
			}
		}
		return elementName(name, t)
	}
	wrapElement(a, t)
	a.unmarshal = func(us *unmarshalState, va addressableValue) error {
		if once.Do(init); ferr != nil {
			return ferr
		}
		return us.unmarshalStruct(fields, va)
	}
	return a
}

func (ms *marshalState) omit(f *field, fv addressableValue) bool {
	if f.flags&fOmitEmpty != 0 && isEmpty(fv.Value) {
		return true
	}
	return (f.flags&fOmitZero != 0 || ms.opts.OmitZeroFields) && isZero(fv)
}

// resolve returns the name of an element written in the content of the
// current element: a name without a namespace takes the current element's
// namespace in a qualified struct.
func (ms *marshalState) resolve(n xmltext.Name) xmltext.Name {
	if n.Space == "" && ms.qual {
		n.Space = ms.elemSpace
	}
	return n
}

func (ms *marshalState) marshalStruct(sf *structFields, va addressableValue) error {
	prev := ms.qual
	qual := sf.qualified(prev)
	if qual && ms.elemSpace != "" {
		export.PreferDefaultNamespace(ms.enc)
	}
	ms.qual = qual
	err := ms.marshalFields(sf, va)
	ms.qual = prev
	return err
}

func (ms *marshalState) marshalFields(sf *structFields, va addressableValue) error {
	enc := ms.enc
	for _, i := range sf.attrs {
		f := &sf.list[i]
		fv, ok := fieldValue(va, f.index, false)
		if !ok || isNil(fv) || ms.omit(f, fv) {
			continue
		}
		if isAttrType(f.typ) {
			a := getAttr(fv.Value)
			if a.Name.Local != "" {
				if err := enc.Attr(a.Name, a.Value); err != nil {
					return err
				}
			}
			continue
		}
		b, err := ms.appendTextWith(ms.buf[:0], fv, f.fncs)
		ms.buf = b[:0]
		if err != nil {
			return newMarshalError(ms, f.name, f.typ, err)
		}
		if err := enc.AttrBytes(f.name, b); err != nil {
			return err
		}
	}
	if sf.anyAttr >= 0 {
		f := &sf.list[sf.anyAttr]
		if fv, ok := fieldValue(va, f.index, false); ok {
			if err := ms.marshalAnyAttr(f, fv); err != nil {
				return err
			}
		}
	}

	// The content, in field order. Fields with a parent chain share the
	// parent elements they have in common with the previous field.
	var open []string // the parent elements open
	for i := range sf.list {
		f := &sf.list[i]
		if f.flags&fAttr != 0 {
			continue
		}
		fv, ok := fieldValue(va, f.index, false)
		if !ok {
			continue
		}
		// Close the parent elements not shared with this field, as
		// encoding/xml does, even if the field is then omitted.
		keep := 0
		if f.flags&fElement != 0 {
			for keep < len(open) && keep < len(f.parents) && open[keep] == f.parents[keep] {
				keep++
			}
		}
		for range len(open) - keep {
			if err := enc.EndElement(); err != nil {
				return err
			}
		}
		open = open[:keep]
		switch f.flags & fMode {
		case fCharData, fCDATA:
			if isNil(fv) {
				continue
			}
			b, err := ms.appendRawText(fv)
			if err != nil {
				return newMarshalError(ms, f.name, f.typ, err)
			}
			if f.flags&fCDATA != 0 {
				err = enc.CData(b)
			} else {
				err = writeText(enc, b)
			}
			if err != nil {
				return err
			}
		case fComment:
			if isNil(fv) {
				continue
			}
			b, err := ms.appendRawText(fv)
			if err != nil {
				return newMarshalError(ms, f.name, f.typ, err)
			}
			if len(b) > 0 {
				if err := enc.Comment(b); err != nil {
					return err
				}
			}
		case fInnerXML:
			v := fv.Value
			for v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
				if v.IsNil() {
					break
				}
				v = v.Elem()
			}
			switch {
			case v.Kind() == reflect.String:
				if err := enc.WriteRaw(unsafeBytes(v.String())); err != nil {
					return err
				}
			case v.Kind() == reflect.Slice && v.Type().Elem().Kind() == reflect.Uint8:
				if err := enc.WriteRaw(v.Bytes()); err != nil {
					return err
				}
			}
		default: // element
			if ms.omit(f, fv) || isNil(fv) {
				continue
			}
			for _, p := range f.parents[keep:] {
				if err := enc.BeginElement(ms.resolve(xmltext.Name{Local: p})); err != nil {
					return err
				}
			}
			open = f.parents
			if err := ms.marshalWith(f.name, fv, f.fncs); err != nil {
				return err
			}
		}
	}
	for range open {
		if err := enc.EndElement(); err != nil {
			return err
		}
	}
	return nil
}

// appendRawText returns the text of a chardata, cdata or comment field: a
// string or byte slice as is, any other type in its text form. The result
// is valid until the next use of ms.buf.
func (ms *marshalState) appendRawText(fv addressableValue) ([]byte, error) {
	switch {
	case fv.Kind() == reflect.String:
		return unsafeBytes(fv.String()), nil
	case fv.Type() == bytesType:
		return fv.Bytes(), nil
	}
	b, err := ms.appendTextValue(ms.buf[:0], fv)
	ms.buf = b[:0]
	return b, err
}

func (ms *marshalState) marshalAnyAttr(f *field, fv addressableValue) error {
	switch {
	case isAttrType(f.typ):
		if a := getAttr(fv.Value); a.Name.Local != "" && !ms.skipDecl(a) {
			return ms.enc.Attr(a.Name, a.Value)
		}
	case f.typ.Kind() == reflect.Slice && isAttrType(f.typ.Elem()):
		for i := range fv.Len() {
			if a := getAttr(fv.Index(i)); a.Name.Local != "" && !ms.skipDecl(a) {
				if err := ms.enc.Attr(a.Name, a.Value); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// spaceString describes a namespace for an error message.
func spaceString(space string) string {
	if space == "" {
		return "no namespace"
	}
	return "name space " + space
}

// skipDecl reports whether a, from an any,attr field, is a default namespace
// declaration that the element being written cannot carry: an element in no
// namespace cannot declare a default namespace. Such a declaration comes from
// reading <e xmlns="u"> into a struct whose element name has no namespace;
// it is dropped, as it was before declarations were kept.
func (ms *marshalState) skipDecl(a xmltext.Attr) bool {
	return a.Name.Space == xmltext.XMLNSNamespace && a.Name.Local == "xmlns" &&
		a.Value != "" && ms.elemSpace == ""
}

// resolve returns the name an element in the content of the current
// element must have for a field named n; see marshalState.resolve.
func (us *unmarshalState) resolve(n xmltext.Name) xmltext.Name {
	if n.Space == "" && us.qual {
		n.Space = us.elemSpace
	}
	return n
}

func (us *unmarshalState) unmarshalStruct(sf *structFields, va addressableValue) error {
	name := us.name()
	if xn := sf.xmlname; xn != nil {
		if xn.name.Local != "" && xn.name.Local != name.Local {
			return newUnmarshalError(us, name, va.Type(), fmt.Errorf("expected element type <%s> but have <%s>", xn.name.Local, name.Local))
		}
		want := us.resolve(xn.name)
		if xn.name.Local != "" && want.Space != name.Space {
			return newUnmarshalError(us, name, va.Type(), fmt.Errorf("expected element <%s> in %s but have %s", xn.name.Local, spaceString(want.Space), spaceString(name.Space)))
		}
		if isNameType(xn.typ) {
			fv, _ := fieldValue(va, xn.index, true)
			setName(fv.Value, name)
		}
	}

	src := us.src

	// Attributes.
	for {
		an, value, ok := us.nextAttr()
		if !ok {
			break
		}
		if isNSDecl(an) {
			// A []Attr any,attr field keeps the declarations, so that
			// marshaling writes the same prefixes and a prefix used only
			// in content (xsi:type="p:T") stays declared.
			if sf.anyAttr >= 0 {
				f := &sf.list[sf.anyAttr]
				if f.typ.Kind() == reflect.Slice && isAttrType(f.typ.Elem()) {
					if an.Space == "" {
						an = xmltext.Name{Space: xmltext.XMLNSNamespace, Local: "xmlns"}
					}
					fv, _ := fieldValue(va, f.index, true)
					n := fv.Len()
					fv.Set(reflect.Append(fv.Value, reflect.Zero(f.typ.Elem())))
					setAttr(fv.Index(n), xmltext.Attr{Name: an, Value: string(value)})
				}
			}
			continue
		}
		matched := false
		for _, i := range sf.attrs {
			f := &sf.list[i]
			if f.name.Local != an.Local || f.name.Space != an.Space {
				continue
			}
			fv, _ := fieldValue(va, f.index, true)
			var err error
			if isAttrType(f.typ) {
				setAttr(fv.Value, xmltext.Attr{Name: an, Value: string(value)})
			} else {
				err = us.parseTextWith(value, fv, f.fncs)
			}
			if err != nil {
				return newUnmarshalError(us, an, f.typ, err)
			}
			matched = true
			break
		}
		if matched {
			continue
		}
		if sf.anyAttr >= 0 {
			f := &sf.list[sf.anyAttr]
			fv, _ := fieldValue(va, f.index, true)
			a := xmltext.Attr{Name: an, Value: string(value)}
			switch {
			case isAttrType(f.typ):
				setAttr(fv.Value, a)
				continue
			case f.typ.Kind() == reflect.Slice && isAttrType(f.typ.Elem()):
				n := fv.Len()
				fv.Set(reflect.Append(fv.Value, reflect.Zero(f.typ.Elem())))
				setAttr(fv.Index(n), a)
				continue
			}
		}
		if us.opts.RejectUnknownNames {
			return newUnmarshalError(us, an, va.Type(), ErrUnknownName)
		}
	}

	// Content.
	var text, comment []byte
	if sf.chardata >= 0 {
		text = us.takeBuf()
	}
	if sf.comment >= 0 {
		comment = us.takeBuf()
	}
	// A capture covers the outermost struct with an innerxml field; nested
	// ones read their part of it from where their content starts.
	base := 0
	if sf.innerxml >= 0 {
		if us.captures == 0 {
			src.StartCapture()
		}
		us.captures++
		base = src.CaptureLen()
		defer func() {
			if us.captures--; us.captures == 0 {
				src.StopCapture()
			}
		}()
	}
	// Only element names in the content depend on qualified; attribute
	// names do not. elemSpace is not read unless qual is set.
	prevSpace, prevQual := us.elemSpace, us.qual
	if qual := sf.qualified(prevQual); qual || prevQual {
		us.elemSpace, us.qual = name.Space, qual
	}
	err := us.unmarshalFields(sf, va, nil, base, &text, &comment)
	us.elemSpace, us.qual = prevSpace, prevQual
	if err == nil && sf.chardata >= 0 {
		f := &sf.list[sf.chardata]
		fv, _ := fieldValue(va, f.index, true)
		err = us.setRawText(fv, text)
	}
	if err == nil && sf.comment >= 0 {
		f := &sf.list[sf.comment]
		fv, _ := fieldValue(va, f.index, true)
		err = us.setRawText(fv, comment)
	}
	us.putBuf(text)
	us.putBuf(comment)
	return err
}

// unmarshalFields reads the content of an element through its end tag into
// the fields of sf. path is the chain of parent elements read so far; base
// is where the content starts in the capture for an innerxml field; text
// and comment collect the character data and comments of the struct's own
// element (path empty), if it has fields for them.
func (us *unmarshalState) unmarshalFields(sf *structFields, va addressableValue, path []string, base int, text, comment *[]byte) error {
	src := us.src
	depth := len(path)
	// bare is the namespace of the element names a field tag without a
	// namespace stands for; see structFields.qualified.
	bare := ""
	if us.qual {
		bare = us.elemSpace
	}
	for {
		mark := 0
		if depth == 0 && sf.innerxml >= 0 {
			mark = src.CaptureLen()
		}
		k, err := us.next()
		if err != nil {
			return err
		}
		switch k {
		case xmltext.StartElement:
			n := us.name()
			var f *field
			if bare == "" {
				f = sf.match(n, path)
			} else {
				f = sf.matchQualified(n, bare, path)
			}
			if f != nil {
				fv, _ := fieldValue(va, f.index, true)
				if err := us.unmarshalWith(fv, f.fncs); err != nil {
					return err
				}
				continue
			}
			if f := sf.matchParent(n, bare, path); f != nil {
				if err := us.unmarshalFields(sf, va, f.parents[:depth+1], base, nil, nil); err != nil {
					return err
				}
				continue
			}
			if depth == 0 && sf.anyElem >= 0 {
				f := &sf.list[sf.anyElem]
				fv, _ := fieldValue(va, f.index, true)
				if err := us.unmarshalWith(fv, f.fncs); err != nil {
					return err
				}
				continue
			}
			if us.opts.RejectUnknownNames {
				return newUnmarshalError(us, n, va.Type(), ErrUnknownName)
			}
			if err := us.skipContent(); err != nil {
				return err
			}
		case xmltext.EndElement:
			if depth == 0 && sf.innerxml >= 0 {
				f := &sf.list[sf.innerxml]
				fv, _ := fieldValue(va, f.index, true)
				switch {
				case fv.Kind() == reflect.String:
					fv.SetString(string(src.Captured(base, mark)))
				case fv.Type() == bytesType:
					fv.SetBytes(src.Captured(base, mark))
				}
			}
			return nil
		case xmltext.Text, xmltext.CData:
			if depth == 0 && sf.chardata >= 0 {
				*text = append(*text, us.text()...)
			}
		case xmltext.Comment:
			if depth == 0 && sf.comment >= 0 {
				*comment = append(*comment, us.text()...)
			}
		}
	}
}

// match returns the element field named n at path.
func (sf *structFields) match(n xmltext.Name, path []string) *field {
	for _, i := range sf.elems {
		f := &sf.list[i]
		if f.flags&fAny != 0 || len(f.parents) != len(path) || f.name != n || !hasPath(f, path) {
			continue
		}
		return f
	}
	return nil
}

// matchQualified is match in a qualified struct, where a field tag without
// a namespace names an element in the namespace bare and not one in no
// namespace. It is kept apart so that match stays small enough to inline.
func (sf *structFields) matchQualified(n xmltext.Name, bare string, path []string) *field {
	for _, i := range sf.elems {
		f := &sf.list[i]
		space := f.name.Space
		if space == "" {
			space = bare
		}
		if f.flags&fAny != 0 || len(f.parents) != len(path) || f.name.Local != n.Local || space != n.Space || !hasPath(f, path) {
			continue
		}
		return f
	}
	return nil
}

// matchParent returns a field with parent n after path. Parent elements
// are in the namespace bare (no namespace, or the current element's in a
// qualified struct), as Marshal writes them.
func (sf *structFields) matchParent(n xmltext.Name, bare string, path []string) *field {
	if n.Space != bare {
		return nil
	}
	for _, i := range sf.elems {
		f := &sf.list[i]
		if len(f.parents) > len(path) && f.parents[len(path)] == n.Local && hasPath(f, path) {
			return f
		}
	}
	return nil
}

func hasPath(f *field, path []string) bool {
	for i, p := range path {
		if f.parents[i] != p {
			return false
		}
	}
	return true
}

// setRawText sets a chardata or comment field: a string or byte slice
// directly, any other type from its text form.
func (us *unmarshalState) setRawText(fv addressableValue, b []byte) error {
	switch {
	case fv.Kind() == reflect.String:
		fv.SetString(string(b))
		return nil
	case fv.Type() == bytesType:
		fv.SetBytes(append([]byte{}, b...))
		return nil
	}
	return us.parseTextValue(b, fv)
}
