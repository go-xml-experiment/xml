// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"reflect"
	"sync"

	"github.com/go-xml-experiment/xml/internal/xmllegacy"
)

// typeInfo holds details for the xml representation of a type.
type typeInfo struct {
	xmlname *fieldInfo
	fields  []fieldInfo
}

// fieldInfo holds details for the xml representation of a single field.
type fieldInfo struct {
	idx     []int
	name    string
	xmlns   string
	flags   fieldFlags
	parents []string
}

type fieldFlags int

const (
	fElement fieldFlags = 1 << iota
	fAttr
	fCDATA
	fCharData
	fInnerXML
	fComment
	fAny

	fOmitEmpty

	fMode = fElement | fAttr | fCDATA | fCharData | fInnerXML | fComment | fAny

	xmlName = "XMLName"
)

var tinfoMap sync.Map // map[reflect.Type]*typeInfo

var nameType = reflect.TypeFor[Name]()

// getTypeInfo returns the typeInfo structure with details necessary
// for marshaling and unmarshaling typ. The fields come from
// the v2 package, which marshals with the same rules.
func getTypeInfo(typ reflect.Type) (*typeInfo, error) {
	if ti, ok := tinfoMap.Load(typ); ok {
		return ti.(*typeInfo), nil
	}
	tinfo := &typeInfo{}
	if typ.Kind() == reflect.Struct && typ != nameType {
		xn, fields, err := xmllegacy.StructFields(typ)
		if err != nil {
			return nil, err
		}
		// The flags of xmllegacy have the values of ours; see
		// TestLegacyFieldFlags.
		conv := func(f *xmllegacy.Field) fieldInfo {
			return fieldInfo{idx: f.Index, name: f.Name.Local, xmlns: f.Name.Space, flags: fieldFlags(f.Flags), parents: f.Parents}
		}
		if xn != nil {
			fi := conv(xn)
			tinfo.xmlname = &fi
		}
		tinfo.fields = make([]fieldInfo, len(fields))
		for i := range fields {
			tinfo.fields[i] = conv(&fields[i])
		}
	}
	ti, _ := tinfoMap.LoadOrStore(typ, tinfo)
	return ti.(*typeInfo), nil
}

// A TagPathError represents an error in the unmarshaling process
// caused by the use of field tags with conflicting paths.
type TagPathError struct {
	Struct       reflect.Type
	Field1, Tag1 string
	Field2, Tag2 string
}

func (e *TagPathError) Error() string {
	return fmt.Sprintf("%s field %q with tag %q conflicts with field %q with tag %q", e.Struct, e.Field1, e.Tag1, e.Field2, e.Tag2)
}

const (
	initNilPointers     = true
	dontInitNilPointers = false
)

// value returns v's field value corresponding to finfo.
// It's equivalent to v.FieldByIndex(finfo.idx), but when passed
// initNilPointers, it initializes and dereferences pointers as necessary.
// When passed dontInitNilPointers and a nil pointer is reached, the function
// returns a zero reflect.Value.
func (finfo *fieldInfo) value(v reflect.Value, shouldInitNilPointers bool) reflect.Value {
	for i, x := range finfo.idx {
		if i > 0 {
			t := v.Type()
			if t.Kind() == reflect.Pointer && t.Elem().Kind() == reflect.Struct {
				if v.IsNil() {
					if !shouldInitNilPointers {
						return reflect.Value{}
					}
					v.Set(reflect.New(v.Type().Elem()))
				}
				v = v.Elem()
			}
		}
		v = v.Field(x)
	}
	return v
}
