// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/Clickin/go-experiment-xml/internal/xmllegacy"
	"github.com/Clickin/go-experiment-xml/xmltext"
)

// This file implements the marshaling of encoding/xml (version 1) on the
// field rules, scratch buffers and Encoder of this package, as
// encoding/json/v2 implements encoding/json under its legacy flags. Unlike
// JSON, the two versions of XML differ in most details of how a value
// becomes an element, so the legacy semantics live in functions of their
// own instead of flags inside the default arshalers:
//
//   - Addressability is not forced: a method on *T is used only for an
//     addressable T, and values held in interfaces are not addressable.
//   - Methods are encoding/xml's Marshaler and MarshalerAttr, called through
//     xmllegacy.V1, then encoding.TextMarshaler (preferring AppendText).
//   - Element names, attributes of slices, the xmlns="" override, comments
//     and innerxml follow encoding/xml exactly; errors are its own.

// maxLegacyScratch is the largest scratch buffer, in bytes, that an
// encoding/xml Encoder keeps between calls.
const maxLegacyScratch = 4 << 10

func init() {
	xmllegacy.Marshal = legacyMarshal
	xmllegacy.StructFields = legacyStructFields
	xmllegacy.Unmarshal = legacyUnmarshal
}

// legacyStructFields exports the fields encoding/xml unmarshals with, so
// that its Marshal and Unmarshal share one set of rules.
func legacyStructFields(t reflect.Type) (*xmllegacy.Field, []xmllegacy.Field, error) {
	sf, err := cachedStructFields(t, true)
	if err != nil {
		return nil, nil, err
	}
	conv := func(f *field) xmllegacy.Field {
		var fl uint8
		for _, b := range [...]struct {
			v2 fieldFlags
			v1 uint8
		}{
			{fElement, xmllegacy.FElement}, {fAttr, xmllegacy.FAttr}, {fCDATA, xmllegacy.FCDATA},
			{fCharData, xmllegacy.FCharData}, {fInnerXML, xmllegacy.FInnerXML}, {fComment, xmllegacy.FComment},
			{fAny, xmllegacy.FAny}, {fOmitEmpty, xmllegacy.FOmitEmpty},
		} {
			if f.flags&b.v2 != 0 {
				fl |= b.v1
			}
		}
		return xmllegacy.Field{Index: f.index, Name: f.name, Parents: f.parents, Flags: fl}
	}
	var xn *xmllegacy.Field
	if sf.xmlname != nil {
		x := conv(sf.xmlname)
		xn = &x
	}
	fields := make([]xmllegacy.Field, len(sf.list))
	for i := range sf.list {
		fields[i] = conv(&sf.list[i])
	}
	return xn, fields, nil
}

var noFields = structFields{anyAttr: -1, anyElem: -1, chardata: -1, comment: -1, innerxml: -1, legacy: true}

// legacyState is the part of marshalState used by legacyMarshal.
type legacyState struct {
	legacy bool
	owner  any // the encoding/xml Encoder
	hooks  *xmllegacy.Hooks
	tmpl   *xmllegacy.Start // the start of the value being marshaled, if given
	spaces []string         // namespaces of the elements this call opened
	base   int              // len(spaces) when the current call began
	attrs  []pendingAttr    // the attributes of the start tag being built
	atext  []byte           // formatted attribute values
}

func legacyMarshal(enc *xmltext.Encoder, owner any, v reflect.Value, start *xmllegacy.Start) error {
	slot := export.EncoderState(enc)
	ms, _ := (*slot).(*marshalState)
	if ms == nil || !ms.legacy {
		// The state stays with enc, which keeps it across Reset, for the
		// next call.
		ms = &marshalState{}
		ms.legacy, ms.hooks = true, xmllegacy.V1
		*slot = ms
	}
	ms.enc, ms.owner = enc, owner
	if start != nil && start.Name.Local == "" {
		return errors.New("xml: EncodeElement of StartElement with missing name")
	}
	tmpl, base := ms.tmpl, ms.base
	ms.tmpl, ms.base = start, len(ms.spaces)
	defer func() {
		ms.tmpl, ms.base, ms.spaces = tmpl, base, ms.spaces[:ms.base]
		if base == 0 {
			// The outermost call: keep no caller data, and only small
			// scratch, as encoding/xml did.
			clear(ms.spaces[:cap(ms.spaces)])
			clear(ms.attrs[:cap(ms.attrs)])
			ms.attrs = ms.attrs[:0]
			if cap(ms.buf) > maxLegacyScratch {
				ms.buf = nil
			}
			if cap(ms.atext) > maxLegacyScratch {
				ms.atext = nil
			}
		}
	}()
	if !v.IsValid() {
		return nil
	}
	return ms.legacyValue(xmltext.Name{}, v, nil)
}

// takeTmpl returns the start given for the value being marshaled, which
// applies to it alone.
func (ms *marshalState) takeTmpl() *xmllegacy.Start {
	t := ms.tmpl
	ms.tmpl = nil
	return t
}

// parentSpace returns the namespace of the innermost open element, as
// encoding/xml tracks it: one opened by this call or else by the Encoder's
// owner through EncodeToken.
func (ms *marshalState) parentSpace() string {
	if n := len(ms.spaces); n > ms.base {
		return ms.spaces[n-1]
	}
	return ms.hooks.ParentSpace(ms.owner)
}

// legacyMethods records which interfaces a type T implements, as T and
// as *T.
type legacyMethods uint8

const (
	lmMarshaler legacyMethods = 1 << iota
	lmMarshalerPtr
	lmAttr
	lmAttrPtr
	lmText
	lmTextPtr
	lmAppend
	lmAppendPtr
)

var legacyMethodCache sync.Map // map[reflect.Type]legacyMethods

func (ms *marshalState) methodsOf(t reflect.Type) legacyMethods {
	if v, ok := legacyMethodCache.Load(t); ok {
		return v.(legacyMethods)
	}
	var m legacyMethods
	pt := reflect.PointerTo(t)
	for _, c := range [...]struct {
		iface    reflect.Type
		val, ptr legacyMethods
	}{
		{ms.hooks.MarshalerType, lmMarshaler, lmMarshalerPtr},
		{ms.hooks.MarshalerAttrType, lmAttr, lmAttrPtr},
		{textMarshalerType, lmText, lmTextPtr},
		{textAppenderType, lmAppend, lmAppendPtr},
	} {
		if t.Implements(c.iface) {
			m |= c.val
		}
		if pt.Implements(c.iface) {
			m |= c.ptr
		}
	}
	legacyMethodCache.Store(t, m)
	return m
}

// methodRecv returns the receiver for the iface method of val: its address
// if *T has the method and val is addressable, else val if T has it. For an
// interface value, it is the value if its dynamic type has the method.
func methodRecv(val reflect.Value, m legacyMethods, iface reflect.Type, valBit, ptrBit legacyMethods) (reflect.Value, bool) {
	if val.Kind() == reflect.Interface {
		if val.CanInterface() && !val.IsNil() && val.Elem().Type().Implements(iface) {
			return val, true
		}
		return reflect.Value{}, false
	}
	if m&ptrBit != 0 && val.CanAddr() {
		if pv := val.Addr(); pv.CanInterface() {
			return pv, true
		}
	}
	if m&valBit != 0 && val.CanInterface() {
		return val, true
	}
	return reflect.Value{}, false
}

// appendLegacyText appends the text of recv, an encoding.TextMarshaler,
// calling AppendText if it has one, as encoding/json/v2 does.
func (ms *marshalState) appendLegacyText(dst []byte, recv reflect.Value) ([]byte, error) {
	if recv.Kind() != reflect.Interface && ms.methodsOf(recv.Type())&lmAppend != 0 {
		a, _ := reflect.TypeAssert[encoding.TextAppender](recv)
		return a.AppendText(dst)
	}
	m, _ := reflect.TypeAssert[encoding.TextMarshaler](recv)
	text, err := m.MarshalText()
	if err != nil {
		return dst, err
	}
	return append(dst, text...), nil
}

// legacyStart is encoding/xml's defaultStart: the start of a value that
// marshals itself.
func legacyStart(t reflect.Type, name xmltext.Name, tmpl *xmllegacy.Start) xmllegacy.Start {
	switch {
	case tmpl != nil:
		return xmllegacy.Start{Name: tmpl.Name, Attr: append([]xmltext.Attr(nil), tmpl.Attr...)}
	case name.Local != "":
		return xmllegacy.Start{Name: name}
	case t.Name() != "":
		return xmllegacy.Start{Name: xmltext.Name{Local: t.Name()}}
	}
	return xmllegacy.Start{Name: xmltext.Name{Local: t.Elem().Name()}}
}

// legacyValue marshals val as encoding/xml's printer.marshalValue does,
// with name the name of the field holding it, if any, and fi that field.
func (ms *marshalState) legacyValue(name xmltext.Name, val reflect.Value, fi *field) error {
	if fi != nil && fi.flags&fOmitEmpty != 0 && isEmpty(val) {
		return nil
	}
	for val.Kind() == reflect.Interface || val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	typ := val.Type()
	kind := val.Kind()
	m := ms.methodsOf(typ)
	h := ms.hooks

	if m&(lmMarshaler|lmMarshalerPtr) != 0 {
		if m&lmMarshaler != 0 && val.CanInterface() {
			return h.CallMarshaler(ms.owner, val, legacyStart(typ, name, ms.takeTmpl()))
		}
		if m&lmMarshalerPtr != 0 && val.CanAddr() {
			if pv := val.Addr(); pv.CanInterface() {
				return h.CallMarshaler(ms.owner, pv, legacyStart(pv.Type(), name, ms.takeTmpl()))
			}
		}
	}
	if recv, ok := methodRecv(val, m, textMarshalerType, lmText, lmTextPtr); ok {
		startType := typ
		if m&lmText == 0 {
			startType = recv.Type()
		}
		start := legacyStart(startType, name, ms.takeTmpl())
		if err := ms.legacyBegin(start.Name, start.Attr); err != nil {
			return err
		}
		b, err := ms.appendLegacyText(ms.buf[:0], recv)
		ms.buf = b[:0]
		if err == nil {
			err = ms.enc.LegacyText(b, true)
		}
		if err != nil {
			return err
		}
		return ms.legacyEnd()
	}

	if (kind == reflect.Slice || kind == reflect.Array) && typ.Elem().Kind() != reflect.Uint8 {
		tmpl := ms.tmpl
		for i, n := 0, val.Len(); i < n; i++ {
			ms.tmpl = tmpl
			if err := ms.legacyValue(name, val.Index(i), fi); err != nil {
				return err
			}
		}
		ms.tmpl = nil
		return nil
	}

	var sf *structFields
	switch {
	case typ == h.NameType:
		sf = &noFields // encoding/xml gives its Name no fields
	case kind == reflect.Struct:
		var err error
		if sf, err = cachedStructFields(typ, true); err != nil {
			return err
		}
	}

	// The element name: the start given, the XMLName tag, a set XMLName
	// value, the field name, the type name.
	var start xmltext.Name
	var attrs []xmltext.Attr
	if tmpl := ms.takeTmpl(); tmpl != nil {
		start, attrs = tmpl.Name, tmpl.Attr
	} else if sf != nil && sf.xmlname != nil {
		if xn := sf.xmlname; xn.name.Local != "" {
			start = xn.name
		} else if fv, ok := fieldValue(addressableValue{Value: val}, xn.index, false); ok && fv.Type() == h.NameType {
			if n := getName(fv.Value); n.Local != "" {
				start = n
			}
		}
	}
	if start.Local == "" {
		start = name
	}
	if start.Local == "" {
		n := typ.Name()
		if i := strings.IndexByte(n, '['); i >= 0 {
			n = n[:i] // a generic instantiation; see go.dev/issue/48318
		}
		if n == "" {
			return h.UnsupportedType(typ)
		}
		start.Local = n
	}

	// The attributes are collected before anything is written, so that an
	// error leaves no partial start tag.
	ms.attrs, ms.atext = ms.attrs[:0], ms.atext[:0]
	for _, a := range attrs {
		ms.addAttr(a.Name, a.Value)
	}
	if sf != nil {
		for i := range sf.list {
			f := &sf.list[i]
			if f.flags&fAttr == 0 {
				continue
			}
			fv, ok := fieldValue(addressableValue{Value: val}, f.index, false)
			if !ok || f.flags&fOmitEmpty != 0 && isEmpty(fv.Value) || fv.Kind() == reflect.Interface && fv.IsNil() {
				continue
			}
			if err := ms.legacyAttr(f.name, fv.Value); err != nil {
				return err
			}
		}
		// An unnamed XMLName leaves an element without a namespace in its
		// parent's default namespace; encoding/xml undoes that.
		if xn := sf.xmlname; xn != nil && start.Space == "" && xn.name.Space == "" && xn.name.Local == "" && ms.parentSpace() != "" {
			ms.addAttr(xmltext.Name{Local: "xmlns"}, "")
		}
	}
	if err := ms.legacyBegin(start, nil); err != nil {
		return err
	}

	var err error
	switch {
	case kind == reflect.Struct:
		err = ms.legacyStruct(sf, val)
	case kind >= reflect.Bool && kind <= reflect.Float64:
		var b []byte
		b, err = appendSimple(ms.buf[:0], val)
		ms.buf = b[:0]
		if err == nil {
			err = ms.enc.LegacyText(b, true)
		}
	case kind == reflect.String:
		err = ms.enc.LegacyText(unsafeBytes(val.String()), true)
	case kind == reflect.Slice && typ.Elem().Kind() == reflect.Uint8:
		err = ms.enc.LegacyText(val.Bytes(), true)
	case kind == reflect.Array && typ.Elem().Kind() == reflect.Uint8:
		if val.CanAddr() {
			err = ms.enc.LegacyText(val.Bytes(), true)
		} else {
			b := append(ms.buf[:0], make([]byte, val.Len())...)
			reflect.Copy(reflect.ValueOf(b), val)
			ms.buf = b[:0]
			err = ms.enc.LegacyText(b, true)
		}
	default:
		err = h.UnsupportedType(typ)
	}
	if err != nil {
		return err
	}
	return ms.legacyEnd()
}

func (ms *marshalState) legacyBegin(name xmltext.Name, attrs []xmltext.Attr) error {
	if name.Local == "" {
		return errors.New("xml: start tag with no name")
	}
	if err := ms.enc.BeginElement(name); err != nil {
		return err
	}
	ms.spaces = append(ms.spaces, name.Space)
	for _, a := range attrs {
		if a.Name.Local != "" {
			if err := ms.enc.Attr(a.Name, a.Value); err != nil {
				return err
			}
		}
	}
	for i := range ms.attrs {
		a := &ms.attrs[i]
		var err error
		if a.inText {
			err = ms.enc.AttrBytes(a.name, ms.atext[a.off:a.end])
		} else {
			err = ms.enc.Attr(a.name, a.s)
		}
		if err != nil {
			return err
		}
	}
	ms.attrs, ms.atext = ms.attrs[:0], ms.atext[:0]
	return ms.enc.CloseStartTag()
}

// pendingAttr is an attribute collected by legacyAttr; a formatted value
// is in atext[off:end] instead of a string of its own.
type pendingAttr struct {
	name     xmltext.Name
	s        string
	off, end int
	inText   bool
}

func (ms *marshalState) addAttr(name xmltext.Name, value string) {
	if name.Local != "" {
		ms.attrs = append(ms.attrs, pendingAttr{name: name, s: value})
	}
}

// addAttrText records an attribute whose value was appended to atext at off.
func (ms *marshalState) addAttrText(name xmltext.Name, off int) {
	ms.attrs = append(ms.attrs, pendingAttr{name: name, off: off, end: len(ms.atext), inText: true})
}

func (ms *marshalState) legacyEnd() error {
	ms.spaces = ms.spaces[:len(ms.spaces)-1]
	return ms.enc.EndElement()
}

// appendSimple appends the text of a basic value as encoding/xml formats it.
func appendSimple(dst []byte, val reflect.Value) ([]byte, error) {
	switch val.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(dst, val.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return strconv.AppendUint(dst, val.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		return strconv.AppendFloat(dst, val.Float(), 'g', -1, val.Type().Bits()), nil
	case reflect.String:
		return append(dst, val.String()...), nil
	case reflect.Bool:
		return strconv.AppendBool(dst, val.Bool()), nil
	case reflect.Array:
		if val.Type().Elem().Kind() == reflect.Uint8 {
			n := len(dst)
			dst = append(dst, make([]byte, val.Len())...)
			reflect.Copy(reflect.ValueOf(dst[n:]), val)
			return dst, nil
		}
	case reflect.Slice:
		if val.Type().Elem().Kind() == reflect.Uint8 {
			return append(dst, val.Bytes()...), nil
		}
	}
	return dst, xmllegacy.V1.UnsupportedType(val.Type())
}

// legacyAttr writes the attribute of a field, as encoding/xml's
// printer.marshalAttr does.
func (ms *marshalState) legacyAttr(name xmltext.Name, val reflect.Value) error {
	m := ms.methodsOf(val.Type())
	if recv, ok := methodRecv(val, m, ms.hooks.MarshalerAttrType, lmAttr, lmAttrPtr); ok {
		a, err := ms.hooks.CallMarshalerAttr(recv, name)
		if err != nil {
			return err
		}
		ms.addAttr(a.Name, a.Value)
		return nil
	}
	if recv, ok := methodRecv(val, m, textMarshalerType, lmText, lmTextPtr); ok {
		off := len(ms.atext)
		b, err := ms.appendLegacyText(ms.atext, recv)
		if err != nil {
			return err
		}
		ms.atext = b
		ms.addAttrText(name, off)
		return nil
	}
	switch val.Kind() {
	case reflect.Pointer, reflect.Interface:
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	if val.Kind() == reflect.Slice && val.Type().Elem().Kind() != reflect.Uint8 {
		for i, n := 0, val.Len(); i < n; i++ {
			if err := ms.legacyAttr(name, val.Index(i)); err != nil {
				return err
			}
		}
		return nil
	}
	if val.Type() == ms.hooks.AttrType {
		a := getAttr(val)
		ms.addAttr(a.Name, a.Value)
		return nil
	}
	if val.Kind() == reflect.String {
		ms.attrs = append(ms.attrs, pendingAttr{name: name, s: val.String()})
		return nil
	}
	off := len(ms.atext)
	b, err := appendSimple(ms.atext, val)
	if err != nil {
		return err
	}
	ms.atext = b
	ms.addAttrText(name, off)
	return nil
}

// indirect drills into interfaces and pointers, stopping at a nil one.
func indirect(v reflect.Value) reflect.Value {
	for v.Kind() == reflect.Interface || v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return v
		}
		v = v.Elem()
	}
	return v
}

// legacyStruct writes the content of a struct as encoding/xml's
// printer.marshalStruct does.
func (ms *marshalState) legacyStruct(sf *structFields, val reflect.Value) error {
	enc := ms.enc
	var open []string // the parent elements open
	trim := func(parents []string) error {
		keep := 0
		for keep < len(open) && keep < len(parents) && open[keep] == parents[keep] {
			keep++
		}
		for range len(open) - keep {
			if err := ms.legacyEnd(); err != nil {
				return err
			}
		}
		open = open[:keep]
		return nil
	}
	for i := range sf.list {
		f := &sf.list[i]
		if f.flags&fAttr != 0 {
			continue
		}
		afv, ok := fieldValue(addressableValue{Value: val}, f.index, false)
		if !ok {
			continue
		}
		vf := afv.Value

		switch f.flags & fMode {
		case fCDATA, fCharData:
			emit := func(b []byte) error { return enc.LegacyText(b, true) }
			if f.flags&fMode == fCDATA {
				emit = enc.CData
			}
			if err := trim(f.parents); err != nil {
				return err
			}
			if recv, ok := methodRecv(vf, ms.methodsOf(vf.Type()), textMarshalerType, lmText, lmTextPtr); ok {
				b, err := ms.appendLegacyText(ms.buf[:0], recv)
				ms.buf = b[:0]
				if err == nil {
					err = emit(b)
				}
				if err != nil {
					return err
				}
				continue
			}
			vf = indirect(vf)
			switch vf.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
				reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr,
				reflect.Float32, reflect.Float64, reflect.Bool, reflect.String:
				b, _ := appendSimple(ms.buf[:0], vf)
				ms.buf = b[:0]
				if err := emit(b); err != nil {
					return err
				}
			case reflect.Slice:
				if vf.CanInterface() {
					if b, ok := reflect.TypeAssert[[]byte](vf); ok {
						if err := emit(b); err != nil {
							return err
						}
					}
				}
			}
			continue

		case fComment:
			if err := trim(f.parents); err != nil {
				return err
			}
			vf = indirect(vf)
			k := vf.Kind()
			if !(k == reflect.String || k == reflect.Slice && vf.Type().Elem().Kind() == reflect.Uint8) {
				return fmt.Errorf("xml: bad type for comment field of %s", val.Type())
			}
			if vf.Len() == 0 {
				continue
			}
			var b []byte
			if k == reflect.String {
				b = unsafeBytes(vf.String())
			} else {
				b = vf.Bytes()
			}
			enc.LegacyIndent(0)
			enc.WriteRaw([]byte("<!--"))
			if bytes.Contains(b, []byte("--")) {
				return errors.New(`xml: comments must not contain "--"`)
			}
			enc.WriteRaw(b)
			if b[len(b)-1] == '-' {
				// "--->" is invalid grammar. Make it "- -->"
				enc.WriteRaw([]byte(" "))
			}
			if err := enc.WriteRaw([]byte("-->")); err != nil {
				return err
			}
			continue

		case fInnerXML:
			vf = indirect(vf)
			switch vf.Type() {
			case bytesType:
				if err := enc.WriteRaw(vf.Bytes()); err != nil {
					return err
				}
				continue
			case stringType:
				if err := enc.WriteRaw(unsafeBytes(vf.String())); err != nil {
					return err
				}
				continue
			}

		case fElement, fElement | fAny:
			if err := trim(f.parents); err != nil {
				return err
			}
			if len(f.parents) > len(open) {
				if vf.Kind() != reflect.Pointer && vf.Kind() != reflect.Interface || !vf.IsNil() {
					for _, p := range f.parents[len(open):] {
						if err := ms.legacyBegin(xmltext.Name{Local: p}, nil); err != nil {
							return err
						}
					}
					open = f.parents
				}
			}
		}
		if err := ms.legacyValue(f.name, vf, f); err != nil {
			return err
		}
	}
	return trim(nil)
}
