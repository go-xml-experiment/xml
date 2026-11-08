// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/Clickin/go-experiment-xml/internal/xmllegacy"
	"github.com/Clickin/go-experiment-xml/xmltext"
)

// The struct tag grammar is that of encoding/xml:
//
//	`xml:"[namespace-URI ][parent>...>]name[,flag...]"`
//
// with the flags attr, chardata, cdata, innerxml, comment, any, omitempty
// and, new here, omitzero and (on XMLName) qualified.

type fieldFlags uint16

const (
	fElement fieldFlags = 1 << iota
	fAttr
	fCDATA
	fCharData
	fInnerXML
	fComment
	fAny

	fOmitEmpty
	fOmitZero

	fQualified // on XMLName: unqualified element names take the element's namespace

	fMode = fElement | fAttr | fCDATA | fCharData | fInnerXML | fComment | fAny
)

const xmlNameField = "XMLName"

// field is one struct field as seen by XML.
type field struct {
	index   []int // for reflect.Value.FieldByIndex; len > 1 through embedding
	typ     reflect.Type
	name    xmltext.Name // Space is the namespace from the tag, if any
	parents []string     // a>b>name: [a b]
	flags   fieldFlags

	fncs *arshaler // the arshaler of typ; nil under legacy rules

	// Under legacy rules, the unmarshalMethods of typ and of its slice items.
	um, umItem unmarshalMethods
}

// structFields is the XML view of a struct type.
type structFields struct {
	xmlname *field // XMLName, nil if none
	list    []field

	// Indexes into list, by role.
	attrs    []int
	anyAttr  int // -1 if none
	elems    []int
	anyElem  int // -1 if none
	chardata int // first chardata or cdata field, -1 if none
	comment  int // first comment field, -1 if none
	innerxml int // first innerxml field, -1 if none

	legacy bool // encoding/xml rules; see cachedStructFields
}

type structFieldsKey struct {
	t      reflect.Type
	legacy bool
}

type structFieldsEntry struct {
	sf  *structFields
	err error
}

var structFieldsCache sync.Map // map[structFieldsKey]structFieldsEntry

// cachedStructFields returns the fields of struct type t; legacy selects
// the rules of encoding/xml, under which an embedded struct is inlined even
// if its field has a name tag.
func cachedStructFields(t reflect.Type, legacy bool) (*structFields, error) {
	k := structFieldsKey{t, legacy}
	if v, ok := structFieldsCache.Load(k); ok {
		e := v.(structFieldsEntry)
		return e.sf, e.err
	}
	sf, err := makeStructFields(t, legacy)
	structFieldsCache.Store(k, structFieldsEntry{sf, err})
	return sf, err
}

func makeStructFields(t reflect.Type, legacy bool) (*structFields, error) {
	sf := &structFields{anyAttr: -1, anyElem: -1, chardata: -1, comment: -1, innerxml: -1, legacy: legacy}
	if err := sf.add(t, t, nil); err != nil {
		return nil, err
	}
	for i := range sf.list {
		f := &sf.list[i]
		switch {
		case f.flags&fAttr != 0 && f.flags&fAny != 0:
			if sf.anyAttr < 0 {
				sf.anyAttr = i
			}
		case f.flags&fAttr != 0:
			sf.attrs = append(sf.attrs, i)
		case f.flags&fAny != 0:
			if sf.anyElem < 0 {
				sf.anyElem = i
			}
			sf.elems = append(sf.elems, i)
		case f.flags&fElement != 0:
			sf.elems = append(sf.elems, i)
		case f.flags&(fCharData|fCDATA) != 0:
			if sf.chardata < 0 {
				sf.chardata = i
			}
		case f.flags&fComment != 0:
			if sf.comment < 0 {
				sf.comment = i
			}
		case f.flags&fInnerXML != 0:
			if sf.innerxml < 0 {
				sf.innerxml = i
			}
		}
	}
	if !legacy {
		// As encoding/json/v2 does, look up the arshalers of the fields
		// once. An arshaler resolves the types it holds only when first
		// used, so a recursive type does not recurse here.
		for i := range sf.list {
			sf.list[i].fncs = lookupArshaler(sf.list[i].typ)
		}
	} else {
		for i := range sf.list {
			f := &sf.list[i]
			f.um, f.umItem = unmarshalMethodsOf(f.typ), unmarshalItemMethods(f.typ)
		}
	}
	return sf, nil
}

// add adds the fields of t, which is root or a struct embedded in it at
// index prefix.
func (sf *structFields) add(root, t reflect.Type, prefix []int) error {
	for i := range t.NumField() {
		f := t.Field(i)
		tag := f.Tag.Get("xml")
		if (!f.IsExported() && !f.Anonymous) || tag == "-" {
			continue
		}
		index := append(prefix[:len(prefix):len(prefix)], i)
		if f.Anonymous && (tag == "" || sf.legacy) {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				inner := &structFields{legacy: sf.legacy}
				if err := inner.add(root, et, index); err != nil {
					return err
				}
				if sf.xmlname == nil && inner.xmlname != nil {
					sf.xmlname = inner.xmlname
					if sf.legacy {
						// encoding/xml keeps the index the XMLName field
						// has in the struct declaring it, which so names
						// a field of this one; its value is then only used
						// if that field happens to be a Name.
						x := *inner.xmlname
						x.index = x.index[len(x.index)-1:]
						sf.xmlname = &x
					}
				}
				for j := range inner.list {
					if err := sf.addField(root, &inner.list[j]); err != nil {
						return err
					}
				}
				continue
			}
		}
		if !f.IsExported() && !sf.legacy {
			continue // encoding/xml keeps an unexported embedded non-struct
		}
		fi, err := parseField(t, &f, sf.legacy)
		if err != nil {
			return err
		}
		fi.index = index
		if f.Name == xmlNameField {
			sf.xmlname = fi // a field of t outranks one of an embedded struct
			continue
		}
		if err := sf.addField(root, fi); err != nil {
			return err
		}
	}
	return nil
}

// parseField parses the tag of f, a field of t. With legacy, the option
// omitzero is unknown (and so ignored) as in encoding/xml.
func parseField(t reflect.Type, f *reflect.StructField, legacy bool) (*field, error) {
	fi := &field{typ: f.Type}
	tag := f.Tag.Get("xml")
	if ns, rest, ok := strings.Cut(tag, " "); ok {
		fi.name.Space, tag = ns, rest
	}
	tag, opts, hasOpts := strings.Cut(tag, ",")
	if !hasOpts {
		fi.flags = fElement
	} else {
		for opt := range strings.SplitSeq(opts, ",") {
			switch opt {
			case "attr":
				fi.flags |= fAttr
			case "cdata":
				fi.flags |= fCDATA
			case "chardata":
				fi.flags |= fCharData
			case "innerxml":
				fi.flags |= fInnerXML
			case "comment":
				fi.flags |= fComment
			case "any":
				fi.flags |= fAny
			case "omitempty":
				fi.flags |= fOmitEmpty
			case "omitzero":
				if !legacy {
					fi.flags |= fOmitZero
				}
			case "qualified":
				if !legacy {
					fi.flags |= fQualified
				}
			}
		}
		valid := true
		switch mode := fi.flags & fMode; mode {
		case 0:
			fi.flags |= fElement
		case fAttr, fCDATA, fCharData, fInnerXML, fComment, fAny, fAny | fAttr:
			if f.Name == xmlNameField || tag != "" && mode != fAttr {
				valid = false
			}
		default:
			valid = false
		}
		if fi.flags&fMode == fAny {
			fi.flags |= fElement
		}
		if fi.flags&(fOmitEmpty|fOmitZero) != 0 && fi.flags&(fElement|fAttr) == 0 {
			valid = false
		}
		if fi.flags&fQualified != 0 && f.Name != xmlNameField {
			valid = false
		}
		if !valid {
			return nil, fmt.Errorf("xml: invalid tag in field %s of type %s: %q", f.Name, t, f.Tag.Get("xml"))
		}
	}
	if fi.name.Space != "" && tag == "" {
		return nil, fmt.Errorf("xml: namespace without name in field %s of type %s: %q", f.Name, t, f.Tag.Get("xml"))
	}
	if f.Name == xmlNameField {
		fi.name.Local = tag
		return fi, nil
	}
	if tag == "" {
		if xn := typeXMLName(f.Type); xn != nil && (fi.flags&fElement != 0 || legacy) {
			fi.name = xn.name
		} else {
			fi.name.Local = f.Name
		}
		return fi, nil
	}
	parents := strings.Split(tag, ">")
	if parents[0] == "" {
		parents[0] = f.Name
	}
	if parents[len(parents)-1] == "" {
		return nil, fmt.Errorf("xml: trailing '>' in field %s of type %s", f.Name, t)
	}
	fi.name.Local = parents[len(parents)-1]
	if len(parents) > 1 {
		if fi.flags&fElement == 0 {
			return nil, fmt.Errorf("xml: %s chain not valid with %s flag", tag, opts)
		}
		fi.parents = parents[:len(parents)-1]
	}
	if fi.flags&fElement != 0 {
		if xn := typeXMLName(f.Type); xn != nil {
			if xn.name.Local != fi.name.Local {
				return nil, fmt.Errorf("xml: name %q in tag of %s.%s conflicts with name %q in %s.XMLName",
					fi.name.Local, t, f.Name, xn.name.Local, f.Type)
			}
			if !legacy {
				// Marshal writes the element with the name of the type's
				// XMLName tag, namespace included, so that is the name to
				// match.
				fi.name.Space = xn.name.Space
			}
		} else if xn := typeXMLName(itemType(f.Type)); xn != nil && xn.name.Local == fi.name.Local && !legacy {
			// The same for the items of a slice or array.
			fi.name.Space = xn.name.Space
		}
	}
	return fi, nil
}

// qualified reports whether the element names without a namespace in sf
// take the namespace of the element sf is read from or written as, given
// whether those of the enclosing struct do. An XMLName tag with qualified
// turns this on, and one with a namespace but without qualified turns it
// off, like a JAXB package's elementFormDefault; a struct that names no
// namespace inherits it.
func (sf *structFields) qualified(inherited bool) bool {
	if xn := sf.xmlname; xn != nil {
		if xn.flags&fQualified != 0 {
			return true
		}
		if xn.name.Space != "" {
			return false
		}
	}
	return inherited
}

// itemType returns the item type of a slice or array type t, after
// pointers, other than a byte slice; or nil.
func itemType(t reflect.Type) reflect.Type {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if (t.Kind() == reflect.Slice || t.Kind() == reflect.Array) && t.Elem().Kind() != reflect.Uint8 {
		return t.Elem()
	}
	return nil
}

// typeXMLName returns the XMLName field of t, after pointers, if it names
// an element.
func typeXMLName(t reflect.Type) *field {
	if t == nil {
		return nil
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	f, ok := t.FieldByName(xmlNameField)
	if !ok || len(f.Index) != 1 {
		return nil
	}
	fi, err := parseField(t, &f, false)
	if err != nil || fi.name.Local == "" {
		return nil
	}
	return fi
}

// addField adds nf unless it conflicts with a field at a shallower depth,
// following Go's embedding rules as encoding/xml does. Two fields conflict
// when one's path is a prefix of the other's or they are equal.
func (sf *structFields) addField(root reflect.Type, nf *field) error {
	var conflicts []int
outer:
	for i := range sf.list {
		of := &sf.list[i]
		if of.flags&fMode != nf.flags&fMode {
			continue
		}
		if of.name.Space != "" && nf.name.Space != "" && of.name.Space != nf.name.Space {
			continue
		}
		n := min(len(nf.parents), len(of.parents))
		for p := range n {
			if of.parents[p] != nf.parents[p] {
				continue outer
			}
		}
		switch {
		case len(of.parents) > len(nf.parents):
			if of.parents[len(nf.parents)] == nf.name.Local {
				conflicts = append(conflicts, i)
			}
		case len(of.parents) < len(nf.parents):
			if nf.parents[len(of.parents)] == of.name.Local {
				conflicts = append(conflicts, i)
			}
		default:
			if nf.name == of.name {
				conflicts = append(conflicts, i)
			}
		}
	}
	if conflicts == nil {
		sf.list = append(sf.list, *nf)
		return nil
	}
	for _, i := range conflicts {
		if len(sf.list[i].index) < len(nf.index) {
			return nil // a shallower field wins
		}
	}
	for _, i := range conflicts {
		if of := &sf.list[i]; len(of.index) == len(nf.index) {
			f1, f2 := root.FieldByIndex(of.index), root.FieldByIndex(nf.index)
			if sf.legacy {
				return xmllegacy.V1.TagPathError(root, f1.Name, f1.Tag.Get("xml"), f2.Name, f2.Tag.Get("xml"))
			}
			return fmt.Errorf("xml: %s field %q with tag %q conflicts with field %q with tag %q",
				root, f1.Name, f1.Tag.Get("xml"), f2.Name, f2.Tag.Get("xml"))
		}
	}
	for c := len(conflicts) - 1; c >= 0; c-- {
		i := conflicts[c]
		sf.list = append(sf.list[:i], sf.list[i+1:]...)
	}
	sf.list = append(sf.list, *nf)
	return nil
}

// fieldValue returns the field of struct value v at index. For marshaling
// (alloc false) it reports false at a nil embedded pointer; for
// unmarshaling it allocates one.
func fieldValue(v addressableValue, index []int, alloc bool) (addressableValue, bool) {
	if len(index) == 1 {
		return addressableValue{v.Field(index[0]), v.forcedAddr}, true
	}
	for i, x := range index {
		if i > 0 && v.Kind() == reflect.Pointer {
			if v.IsNil() {
				if !alloc {
					return addressableValue{}, false
				}
				v.Set(reflect.New(v.Type().Elem()))
			}
			v = addressableValue{v.Elem(), false}
		}
		v = addressableValue{v.Field(x), v.forcedAddr}
	}
	return v, true
}
