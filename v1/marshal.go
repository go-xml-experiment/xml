// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"encoding"
	"errors"
	"fmt"
	"io"
	"reflect"
	"unsafe"

	_ "github.com/Clickin/go-experiment-xml" // implements Marshal; see printer.marshal
	"github.com/Clickin/go-experiment-xml/internal/xmllegacy"
	"github.com/Clickin/go-experiment-xml/internal/xmlname"
	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
	"github.com/Clickin/go-experiment-xml/xmltext"
)

const (
	// Header is a generic XML header suitable for use with the output of [Marshal].
	// This is not automatically added to any output of this package,
	// it is provided as a convenience.
	Header = `<?xml version="1.0" encoding="UTF-8"?>` + "\n"
)

// Marshal returns the XML encoding of v.
//
// Marshal handles an array or slice by marshaling each of the elements.
// Marshal handles a pointer by marshaling the value it points at or, if the
// pointer is nil, by writing nothing. Marshal handles an interface value by
// marshaling the value it contains or, if the interface value is nil, by
// writing nothing. Marshal handles all other data by writing one or more XML
// elements containing the data.
//
// The name for the XML elements is taken from, in order of preference:
//   - the tag on the XMLName field, if the data is a struct
//   - the value of the XMLName field of type [Name]
//   - the tag of the struct field used to obtain the data
//   - the name of the struct field used to obtain the data
//   - the name of the marshaled type
//
// The XML element for a struct contains marshaled elements for each of the
// exported fields of the struct, with these exceptions:
//   - the XMLName field, described above, is omitted.
//   - a field with tag "-" is omitted.
//   - a field with tag "name,attr" becomes an attribute with
//     the given name in the XML element.
//   - a field with tag ",attr" becomes an attribute with the
//     field name in the XML element.
//   - a field with tag ",chardata" is written as character data,
//     not as an XML element.
//   - a field with tag ",cdata" is written as character data
//     wrapped in one or more <![CDATA[ ... ]]> tags, not as an XML element.
//   - a field with tag ",innerxml" is written verbatim, not subject
//     to the usual marshaling procedure.
//   - a field with tag ",comment" is written as an XML comment, not
//     subject to the usual marshaling procedure. It must not contain
//     the "--" string within it.
//   - a field with a tag including the "omitempty" option is omitted
//     if the field value is empty. The empty values are false, 0, any
//     nil pointer or interface value, and any array, slice, map, or
//     string of length zero.
//   - an embedded struct field of a concrete type is handled as if
//     the fields of its value were part of the outer struct.
//   - an embedded struct field of an interface type is treated the same as
//     having a field named after the type in the interface value.
//   - a field implementing [Marshaler] is written by calling its MarshalXML
//     method.
//   - a field implementing [encoding.TextMarshaler] is written by encoding the
//     result of its MarshalText method as text.
//
// If a field uses a tag "a>b>c", then the element c will be nested inside
// parent elements a and b. Fields that appear next to each other that name
// the same parent will be enclosed in one XML element.
//
// If the XML name for a struct field is defined by both the field tag and the
// struct's XMLName field, the names must match.
//
// See [MarshalIndent] for an example.
//
// Marshal will return an error if asked to marshal a channel, function, or map.
func Marshal(v any) ([]byte, error) {
	st := getEncoder()
	b, err := st.marshal(v)
	putEncoder(st) // not deferred: a panic leaves the state out of the pool
	return b, err
}

// marshal encodes v and returns the output, which the caller owns.
func (st *encoderState) marshal(v any) ([]byte, error) {
	if err := st.enc.Encode(v); err != nil {
		return nil, err
	}
	if err := st.enc.Close(); err != nil {
		return nil, err
	}
	return st.result(), nil
}

// Marshaler is the interface implemented by objects that can marshal
// themselves into valid XML elements.
//
// MarshalXML encodes the receiver as zero or more XML elements.
// By convention, arrays or slices are typically encoded as a sequence
// of elements, one per entry.
// Using start as the element tag is not required, but doing so
// will enable [Unmarshal] to match the XML elements to the correct
// struct field.
// One common implementation strategy is to construct a separate
// value with a layout corresponding to the desired XML and then
// to encode it using e.EncodeElement.
// Another common strategy is to use repeated calls to e.EncodeToken
// to generate the XML output one token at a time.
// The sequence of encoded tokens must make up zero or more valid
// XML elements.
type Marshaler interface {
	MarshalXML(e *Encoder, start StartElement) error
}

// MarshalerAttr is the interface implemented by objects that can marshal
// themselves into valid XML attributes.
//
// MarshalXMLAttr returns an XML attribute with the encoded value of the receiver.
// Using name as the attribute name is not required, but doing so
// will enable [Unmarshal] to match the attribute to the correct
// struct field.
// If MarshalXMLAttr returns the zero attribute [Attr]{}, no attribute
// will be generated in the output.
// MarshalXMLAttr is used only for struct fields with the
// "attr" option in the field tag.
type MarshalerAttr interface {
	MarshalXMLAttr(name Name) (Attr, error)
}

// MarshalIndent works like [Marshal], but each XML element begins on a new
// indented line that starts with prefix and is followed by one or more
// copies of indent according to the nesting depth.
func MarshalIndent(v any, prefix, indent string) ([]byte, error) {
	st := getEncoder()
	st.enc.Indent(prefix, indent)
	b, err := st.marshal(v)
	putEncoder(st) // not deferred: a panic leaves the state out of the pool
	return b, err
}

// An Encoder writes XML data to an output stream.
type Encoder struct {
	p printer
}

// NewEncoder returns a new encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	e := &Encoder{printer{enc: xmltext.NewEncoder(w, xmlopts.LegacyOutput(true))}}
	e.p.encoder = e
	return e
}

// Indent sets the encoder to generate XML in which each element
// begins on a new indented line that starts with prefix and is followed by
// one or more copies of indent according to the nesting depth.
func (enc *Encoder) Indent(prefix, indent string) {
	enc.p.enc.Indent(prefix, indent)
}

// Encode writes the XML encoding of v to the stream.
//
// See the documentation for [Marshal] for details about the conversion
// of Go values to XML.
//
// Encode calls [Encoder.Flush] before returning.
func (enc *Encoder) Encode(v any) error {
	err := enc.p.marshal(reflect.ValueOf(v), nil)
	if err != nil {
		return err
	}
	return enc.p.enc.Flush()
}

// EncodeElement writes the XML encoding of v to the stream,
// using start as the outermost tag in the encoding.
//
// See the documentation for [Marshal] for details about the conversion
// of Go values to XML.
//
// EncodeElement calls [Encoder.Flush] before returning.
func (enc *Encoder) EncodeElement(v any, start StartElement) error {
	err := enc.p.marshal(reflect.ValueOf(v), &start)
	if err != nil {
		return err
	}
	return enc.p.enc.Flush()
}

var (
	begComment  = []byte("<!--")
	endComment  = []byte("-->")
	endProcInst = []byte("?>")
)

// EncodeToken writes the given XML token to the stream.
// It returns an error if [StartElement] and [EndElement] tokens are not properly matched.
//
// EncodeToken does not call [Encoder.Flush], because usually it is part of a larger operation
// such as [Encoder.Encode] or [Encoder.EncodeElement] (or a custom [Marshaler]'s MarshalXML invoked
// during those), and those will call Flush when finished.
// Callers that create an Encoder and then invoke EncodeToken directly, without
// using Encode or EncodeElement, need to call Flush when finished to ensure
// that the XML is written to the underlying writer.
//
// EncodeToken allows writing a [ProcInst] with Target set to "xml" only as the first token
// in the stream.
func (enc *Encoder) EncodeToken(t Token) error {

	p := &enc.p
	switch t := t.(type) {
	case StartElement:
		if err := p.writeStart(&t); err != nil {
			return err
		}
	case EndElement:
		if err := p.writeEnd(t.Name); err != nil {
			return err
		}
	case CharData:
		if err := p.enc.LegacyText(t, false); err != nil {
			return err
		}
	case Comment:
		if bytes.Contains(t, ddBytes) {
			return fmt.Errorf("xml: EncodeToken of Comment containing -- marker")
		}
		// a trailing "-" gets a space: "--->" is invalid grammar, "- -->" is not
		return p.enc.Comment(t)
	case ProcInst:
		// First token to be encoded which is also a ProcInst with target of xml
		// is the xml declaration. The only ProcInst where target of xml is allowed.
		if t.Target == "xml" && p.enc.Buffered() != 0 {
			return fmt.Errorf("xml: EncodeToken of ProcInst xml target only valid for xml declaration, first token encoded")
		}
		if !xmlname.ValidString(t.Target) {
			return fmt.Errorf("xml: EncodeToken of ProcInst with invalid Target")
		}
		if bytes.Contains(t.Inst, endProcInst) {
			return fmt.Errorf("xml: EncodeToken of ProcInst containing ?> marker")
		}
		if err := p.enc.ProcInst(t.Target, t.Inst); err != nil {
			return err
		}
	case Directive:
		if !isValidDirective(t) {
			return fmt.Errorf("xml: EncodeToken of Directive containing wrong < or > markers")
		}
		if err := p.enc.Directive(t); err != nil {
			return err
		}
	default:
		return fmt.Errorf("xml: EncodeToken of invalid token type")

	}
	return p.cachedWriteError()
}

// isValidDirective reports whether dir is a valid directive text,
// meaning angle brackets are matched, ignoring comments and strings.
func isValidDirective(dir Directive) bool {
	var (
		depth     int
		inquote   uint8
		incomment bool
	)
	for i, c := range dir {
		switch {
		case incomment:
			if c == '>' {
				if n := 1 + i - len(endComment); n >= 0 && bytes.Equal(dir[n:i+1], endComment) {
					incomment = false
				}
			}
			// Just ignore anything in comment
		case inquote != 0:
			if c == inquote {
				inquote = 0
			}
			// Just ignore anything within quotes
		case c == '\'' || c == '"':
			inquote = c
		case c == '<':
			if i+len(begComment) < len(dir) && bytes.Equal(dir[i:i+len(begComment)], begComment) {
				incomment = true
			} else {
				depth++
			}
		case c == '>':
			if depth == 0 {
				return false
			}
			depth--
		}
	}
	return depth == 0 && inquote == 0 && !incomment
}

// Flush flushes any buffered XML to the underlying writer.
// See the [Encoder.EncodeToken] documentation for details about when it is necessary.
func (enc *Encoder) Flush() error {
	return enc.p.enc.Flush()
}

// Close the Encoder, indicating that no more data will be written. It flushes
// any buffered XML to the underlying writer and returns an error if the
// written XML is invalid (e.g. by containing unclosed elements).
func (enc *Encoder) Close() error {
	return enc.p.Close()
}

type printer struct {
	enc         *xmltext.Encoder
	encoder     *Encoder
	tags        []Name
	closed      bool
	hookExposed bool // a custom MarshalXML may retain this encoder
}

// marshalInterface marshals a Marshaler interface value.
func (p *printer) marshalInterface(val Marshaler, start StartElement) error {
	// Push a marker onto the tag stack so that MarshalXML
	// cannot close the XML tags that it did not open.
	p.tags = append(p.tags, Name{})
	n := len(p.tags)

	p.hookExposed = true
	err := val.MarshalXML(p.encoder, start)
	if err != nil {
		return err
	}

	// Make sure MarshalXML closed all its tags. p.tags[n-1] is the mark.
	if len(p.tags) > n {
		return fmt.Errorf("xml: %s.MarshalXML wrote invalid XML: <%s> not closed", receiverType(val), p.tags[len(p.tags)-1].Local)
	}
	p.tags = p.tags[:n-1]
	return nil
}

// writeStart writes the given start element.
func (p *printer) writeStart(start *StartElement) error {
	if start.Name.Local == "" {
		return fmt.Errorf("xml: start tag with no name")
	}

	p.tags = append(p.tags, start.Name)
	if err := p.enc.BeginElement(xmltext.Name{Space: start.Name.Space, Local: start.Name.Local}); err != nil {
		return err
	}
	for _, attr := range start.Attr {
		name := attr.Name
		if name.Local == "" {
			continue
		}
		if err := p.enc.Attr(xmltext.Name{Space: name.Space, Local: name.Local}, attr.Value); err != nil {
			return err
		}
	}
	// the start tag is part of the output (and of a Flush) right away
	return p.enc.CloseStartTag()
}

func (p *printer) writeEnd(name Name) error {
	if name.Local == "" {
		return fmt.Errorf("xml: end tag with no name")
	}
	if len(p.tags) == 0 || p.tags[len(p.tags)-1].Local == "" {
		return fmt.Errorf("xml: end tag </%s> without start tag", name.Local)
	}
	if top := p.tags[len(p.tags)-1]; top != name {
		if top.Local != name.Local {
			return fmt.Errorf("xml: end tag </%s> does not match start tag <%s>", name.Local, top.Local)
		}
		return fmt.Errorf("xml: end tag </%s> in namespace %s does not match start tag <%s> in namespace %s", name.Local, name.Space, top.Local, top.Space)
	}
	p.tags = p.tags[:len(p.tags)-1]
	return p.enc.EndElement()
}

var ddBytes = []byte("--")

// Write implements io.Writer: b is written as is.
func (p *printer) Write(b []byte) (n int, err error) {
	if p.closed {
		return 0, p.enc.Err()
	}
	if err := p.enc.WriteRaw(b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// WriteString implements io.StringWriter
func (p *printer) WriteString(s string) (n int, err error) {
	return p.Write(unsafeBytes(s))
}

// WriteByte implements io.ByteWriter
func (p *printer) WriteByte(c byte) error {
	_, err := p.Write([]byte{c})
	return err
}

// Close the Encoder, indicating that no more data will be written. It flushes
// any buffered XML to the underlying writer and returns an error if the
// written XML is invalid (e.g. by containing unclosed elements).
func (p *printer) Close() error {
	if p.closed {
		return nil
	}
	p.closed = true
	if err := p.enc.Flush(); err != nil {
		return err
	}
	if len(p.tags) > 0 {
		return fmt.Errorf("unclosed tag <%s>", p.tags[len(p.tags)-1].Local)
	}
	return nil
}

// return the output's cached write error
func (p *printer) cachedWriteError() error {
	if p.closed && p.enc.Err() == nil {
		return errors.New("use of closed Encoder")
	}
	return p.enc.Err()
}

// UnsupportedTypeError is returned when [Marshal] encounters a type
// that cannot be converted into XML.
type UnsupportedTypeError struct {
	Type reflect.Type
}

func (e *UnsupportedTypeError) Error() string {
	return "xml: unsupported type: " + e.Type.String()
}

func unsafeBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

var (
	marshalerType     = reflect.TypeFor[Marshaler]()
	marshalerAttrType = reflect.TypeFor[MarshalerAttr]()
	textMarshalerType = reflect.TypeFor[encoding.TextMarshaler]()
)

// marshal writes v with the v2 package, which implements the semantics of
// this package for it; start is the start given to EncodeElement.
func (p *printer) marshal(v reflect.Value, start *StartElement) error {
	var ls *xmllegacy.Start
	if start != nil {
		ls = &xmllegacy.Start{Name: xmltext.Name(start.Name)}
		if len(start.Attr) > 0 {
			ls.Attr = make([]xmltext.Attr, len(start.Attr))
			for i, a := range start.Attr {
				ls.Attr[i] = xmltext.Attr{Name: xmltext.Name(a.Name), Value: a.Value}
			}
		}
	}
	if err := xmllegacy.Marshal(p.enc, p.encoder, v, ls); err != nil {
		return err
	}
	return p.cachedWriteError()
}

func init() {
	xmllegacy.V1 = &xmllegacy.Hooks{
		MarshalerType:     marshalerType,
		MarshalerAttrType: marshalerAttrType,
		NameType:          reflect.TypeFor[Name](),
		AttrType:          attrType,

		UnmarshalerType:     reflect.TypeFor[Unmarshaler](),
		UnmarshalerAttrType: reflect.TypeFor[UnmarshalerAttr](),

		CallMarshaler: func(owner any, recv reflect.Value, start xmllegacy.Start) error {
			m, _ := reflect.TypeAssert[Marshaler](recv)
			s := StartElement{Name: Name(start.Name)}
			if len(start.Attr) > 0 {
				s.Attr = make([]Attr, len(start.Attr))
				for i, a := range start.Attr {
					s.Attr[i] = Attr{Name(a.Name), a.Value}
				}
			}
			return owner.(*Encoder).p.marshalInterface(m, s)
		},
		CallMarshalerAttr: func(recv reflect.Value, name xmltext.Name) (xmltext.Attr, error) {
			m, _ := reflect.TypeAssert[MarshalerAttr](recv)
			a, err := m.MarshalXMLAttr(Name(name))
			return xmltext.Attr{Name: xmltext.Name(a.Name), Value: a.Value}, err
		},
		ParentSpace: func(owner any) string {
			if tags := owner.(*Encoder).p.tags; len(tags) > 0 {
				return tags[len(tags)-1].Space
			}
			return ""
		},
		UnsupportedType: func(t reflect.Type) error { return &UnsupportedTypeError{t} },
		TagPathError: func(t reflect.Type, f1, t1, f2, t2 string) error {
			return &TagPathError{t, f1, t1, f2, t2}
		},
	}
	setUnmarshalHooks(xmllegacy.V1)
}
