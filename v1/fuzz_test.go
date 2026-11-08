// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
)

// The types below cover the v1 field rules that Unmarshal resolves:
// XMLName, attributes, ,any and ,any,attr, chardata, comments, innerxml,
// a>b paths, embedded structs, pointers, slices and the method hooks.

type fuzzEmbedded struct {
	E string `xml:"e"`
	A string `xml:"ea,attr"`
}

type fuzzText struct{ S string }

func (t *fuzzText) UnmarshalText(b []byte) error {
	t.S = "text:" + string(b)
	return nil
}

func (t fuzzText) MarshalText() ([]byte, error) {
	return []byte(strings.TrimPrefix(t.S, "text:")), nil
}

// fuzzDecodeElement decodes itself by calling back into DecodeElement.
type fuzzDecodeElement struct {
	N string   `xml:"n,attr"`
	V string   `xml:",chardata"`
	K []string `xml:"k"`
}

func (e *fuzzDecodeElement) UnmarshalXML(d *Decoder, start StartElement) error {
	type plain fuzzDecodeElement
	return d.DecodeElement((*plain)(e), &start)
}

// fuzzTokens decodes itself by reading tokens up to its end element.
type fuzzTokens struct {
	Names []string
}

func (e *fuzzTokens) UnmarshalXML(d *Decoder, start StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok := tok.(type) {
		case StartElement:
			e.Names = append(e.Names, tok.Name.Local)
		case EndElement:
			if tok.Name == start.Name {
				return nil
			}
		}
	}
}

func (e fuzzTokens) MarshalXML(enc *Encoder, start StartElement) error {
	if err := enc.EncodeToken(start); err != nil {
		return err
	}
	for _, n := range e.Names {
		if err := enc.EncodeElement("", StartElement{Name: Name{Local: n}}); err != nil {
			return err
		}
	}
	return enc.EncodeToken(start.End())
}

type fuzzAttr struct{ V string }

func (a *fuzzAttr) UnmarshalXMLAttr(attr Attr) error {
	a.V = attr.Name.Local + "=" + attr.Value
	return nil
}

type fuzzDoc struct {
	XMLName Name     `xml:"urn:n root"`
	ID      string   `xml:"id,attr"`
	N       int      `xml:"n,attr,omitempty"`
	B       *bool    `xml:"b,attr"`
	NS      string   `xml:"urn:a x,attr"`
	TA      fuzzText `xml:"ta,attr"`
	MA      fuzzAttr `xml:"ma,attr"`
	Rest    []Attr   `xml:",any,attr"`
	fuzzEmbedded
	Items  []string           `xml:"items>item"`
	Deep   string             `xml:"a>b>c"`
	Ptr    *fuzzEmbedded      `xml:"ptr"`
	Ints   []int              `xml:"i"`
	F      float64            `xml:"f,omitempty"`
	U      uint8              `xml:"u"`
	T      fuzzText           `xml:"t"`
	Dec    *fuzzDecodeElement `xml:"dec"`
	Toks   fuzzTokens         `xml:"toks"`
	Bytes  []byte             `xml:"bytes"`
	Iface  any                `xml:"iface"`
	Skip   string             `xml:"-"`
	Any    []fuzzAnyElem      `xml:",any"`
	Char   string             `xml:",chardata"`
	Remark string             `xml:",comment"`
}

type fuzzAnyElem struct {
	XMLName Name
	Attrs   []Attr `xml:",any,attr"`
	Text    string `xml:",chardata"`
}

// fuzzPlainDoc is fuzzDoc without ,any,attr fields. Those do not marshal
// to a fixed point: each round adds the xmlns attributes again.
type fuzzPlainDoc struct {
	XMLName Name     `xml:"urn:n root"`
	ID      string   `xml:"id,attr"`
	N       int      `xml:"n,attr,omitempty"`
	B       *bool    `xml:"b,attr"`
	TA      fuzzText `xml:"ta,attr"`
	fuzzEmbedded
	Items []string           `xml:"items>item"`
	Deep  string             `xml:"a>b>c"`
	Ptr   *fuzzEmbedded      `xml:"ptr"`
	Ints  []int              `xml:"i"`
	U     uint8              `xml:"u"`
	T     fuzzText           `xml:"t"`
	Dec   *fuzzDecodeElement `xml:"dec"`
	Toks  fuzzTokens         `xml:"toks"`
	Bytes []byte             `xml:"bytes"`
	Char  string             `xml:",chardata"`
}

type fuzzInner struct {
	XMLName Name
	ID      string `xml:"id,attr"`
	Inner   string `xml:",innerxml"`
}

// fuzzUnmarshalTargets returns fresh values to unmarshal fuzz input into.
// The first fuzzRoundtripTargets of them marshal to a fixed point.
func fuzzUnmarshalTargets() []any {
	return []any{
		new(fuzzPlainDoc),
		new(fuzzInner),
		new(fuzzDecodeElement),
		new(string),
		new(fuzzDoc),
		new([]fuzzAnyElem),
	}
}

const fuzzRoundtripTargets = 4

// fuzzUnmarshalSummary unmarshals in into each target and describes the
// results, one line per target: the value as JSON, or "-" if it is still
// the zero value, then the error.
func fuzzUnmarshalSummary(in []byte) string {
	var sb strings.Builder
	zero := fuzzUnmarshalTargets()
	for i, v := range fuzzUnmarshalTargets() {
		err := Unmarshal(in, v)
		j, jerr := json.Marshal(v)
		if jerr != nil {
			j = []byte(jerr.Error())
		} else if z, _ := json.Marshal(zero[i]); string(z) == string(j) {
			j = []byte("-")
		}
		sb.Write(j)
		sb.WriteString(" err=")
		if err != nil {
			sb.WriteString(err.Error())
		}
		sb.WriteByte('\n')
	}
	return sb.String()
}

var fuzzUnmarshalSeeds = []string{
	`<root xmlns="urn:n" id="1" n="2" b="true" xmlns:a="urn:a" a:x="ax" ta="tv" ma="mv" other="o" ea="E"><e>emb</e><items><item>one</item><item>two</item></items><a><b><c>deep</c></b></a><ptr ea="p"><e>pe</e></ptr><i>1</i><i>-2</i><f>0.5</f><u>255</u><t>tt</t><dec n="d">dv<k>k1</k></dec><toks><x/><y><z/></y></toks><bytes>raw</bytes><iface>if</iface><extra q="1">ex</extra>chars<!--note--></root>`,
	`<root xmlns="urn:n"><![CDATA[cd]]><?pi x?>&amp;&lt;&#65;</root>`,
	`<root xmlns="urn:wrong"/>`,
	`<x id="i">a<b>c</b><!--d-->e</x>`,
	`<dec n="1"><k>a</k><k>b</k>v</dec>`,
	`<root xmlns="urn:n"><u>256</u></root>`,
	`<root xmlns="urn:n"><i>x</i></root>`,
	`<root xmlns="urn:n" b="maybe"/>`,
	`<root xmlns="urn:n"><items><item>1</item></items><items><item>2</item></items></root>`,
	`<a:root xmlns:a="urn:n"><a:e>p</a:e></a:root>`,
	`<root xmlns="urn:n"><toks><x></toks></root>`,
	`<root xmlns="urn:n"><a><b><c>1</c></b><b><c>2</c></b></a></root>`,
	`<root xmlns="urn:n"></wrong>`,
	`<s>plain</s>`,
}

// FuzzUnmarshal checks that Unmarshal does not panic on arbitrary input,
// and that what it produces marshals to a fixed point:
// Marshal(Unmarshal(Marshal(v))) == Marshal(v).
func FuzzUnmarshal(f *testing.F) {
	for _, s := range fuzzUnmarshalSeeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		ws := fuzzUnmarshalTargets()
		for i, v := range fuzzUnmarshalTargets() {
			if err := Unmarshal(in, v); err != nil || i >= fuzzRoundtripTargets {
				continue
			}
			if v, ok := v.(*fuzzInner); ok && (!fuzzValidName(v.XMLName.Local) || v.XMLName.Local == "xmlns") {
				// Unmarshal takes the local name after a colon as is,
				// so <a:0/> gives a name that Marshal writes as <0>,
				// and <a:xmlns/> one that reads back without its space.
				continue
			}
			b, err := Marshal(v)
			if err != nil {
				continue // e.g. a name or comment that cannot be written
			}
			w := ws[i]
			if err := Unmarshal(b, w); err != nil {
				t.Fatalf("%T: Unmarshal of Marshal output %s: %v", v, b, err)
			}
			b2, err := Marshal(w)
			if err != nil || string(b2) != string(b) {
				t.Fatalf("%T: second Marshal:\n%s\n%s, %v", v, b, b2, err)
			}
		}
	})
}

// fuzzValidName reports whether s reads back as an element name.
func fuzzValidName(s string) bool {
	tok, err := NewDecoder(strings.NewReader("<" + s + "/>")).Token()
	se, ok := tok.(StartElement)
	return err == nil && ok && se.Name.Space == "" && se.Name.Local == s
}

const unmarshalGoldenFile = "testdata/unmarshal_golden.txt"

// TestUnmarshalGolden checks Unmarshal results on the fuzz seeds and on
// inputs found by FuzzUnmarshal against results recorded from the
// standard library's encoding/xml decoder.
func TestUnmarshalGolden(t *testing.T) {
	data, err := os.ReadFile(unmarshalGoldenFile)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct{ in, want string }
	var entries []entry
	for line := range strings.Lines(string(data)) {
		switch {
		case strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "=== "):
			in, err := strconv.Unquote(strings.TrimSpace(line[len("=== "):]))
			if err != nil {
				t.Fatalf("%s: bad input line %s: %v", unmarshalGoldenFile, line, err)
			}
			entries = append(entries, entry{in: in})
		case len(entries) > 0:
			entries[len(entries)-1].want += line
		}
	}

	for _, e := range entries {
		// encoding/json escapes U+FFFD before Go 1.27 and writes it literally after.
		got := strings.ReplaceAll(fuzzUnmarshalSummary([]byte(e.in)), `\ufffd`, "\ufffd")
		if got != e.want {
			t.Errorf("Unmarshal(%q):\ngot:\n%swant:\n%s", e.in, got, e.want)
		}
	}
}
