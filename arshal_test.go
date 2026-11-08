// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/go-xml-experiment/xml/internal/race"
	"github.com/go-xml-experiment/xml/internal/testenv"
	"io"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-xml-experiment/xml/xmltext"
)

type Name = xmltext.Name

type scalars struct {
	XMLName Name    `xml:"s"`
	B       bool    `xml:"b"`
	I       int     `xml:"i"`
	I8      int8    `xml:"i8,attr"`
	U       uint16  `xml:"u"`
	F       float64 `xml:"f"`
	F32     float32 `xml:"f32,attr"`
	S       string  `xml:"s"`
	Bytes   []byte  `xml:"bytes"`
	Arr     [3]byte `xml:"arr"`
	P       *int    `xml:"p"`
	PA      *string `xml:"pa,attr"`
}

type nested struct {
	XMLName  Name      `xml:"urn:n root"`
	ID       string    `xml:"id,attr"`
	Lang     string    `xml:"http://www.w3.org/XML/1998/namespace lang,attr,omitempty"`
	Items    []item    `xml:"items>item"`
	Note     string    `xml:"items>note,omitempty"`
	Text     string    `xml:",chardata"`
	Comment  string    `xml:",comment"`
	When     time.Time `xml:"when"`
	Ptrs     []*item   `xml:"ptr"`
	Embedded           // fields promoted
}

type item struct {
	N   int    `xml:"n,attr"`
	Val string `xml:",chardata"`
}

type Embedded struct {
	E1 string `xml:"e1"`
	E2 int    `xml:"e2,attr"`
}

func ptr[T any](v T) *T { return &v }

func roundtrip[T any](t *testing.T, in T, want string, opts ...Options) {
	t.Helper()
	b, err := Marshal(in, opts...)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want != "" && string(b) != want {
		t.Fatalf("Marshal:\n got %s\nwant %s", b, want)
	}
	var out T
	if err := Unmarshal(b, &out, opts...); err != nil {
		t.Fatalf("Unmarshal(%s): %v", b, err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Fatalf("roundtrip of %s:\n got %+v\nwant %+v", b, out, in)
	}
}

func TestRoundtripScalars(t *testing.T) {
	in := scalars{
		XMLName: Name{Local: "s"},
		B:       true, I: -3, I8: 7, U: 9, F: 1.5, F32: 0.25, S: "a<b&c",
		Bytes: []byte("raw"), Arr: [3]byte{'x', 'y', 'z'}, P: ptr(4), PA: ptr("pa"),
	}
	roundtrip(t, in, `<s i8="7" f32="0.25" pa="pa"><b>true</b><i>-3</i><u>9</u><f>1.5</f><s>a&lt;b&amp;c</s><bytes>raw</bytes><arr>xyz</arr><p>4</p></s>`)
}

func TestRoundtripNested(t *testing.T) {
	in := nested{
		XMLName: Name{Space: "urn:n", Local: "root"},
		ID:      "x1",
		Items:   []item{{1, "one"}, {2, "two"}},
		Text:    "hello",
		Comment: " c ",
		When:    time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
		Ptrs:    []*item{{N: 3}},
		Embedded: Embedded{
			E1: "e", E2: 5,
		},
	}
	roundtrip(t, in, `<ns1:root xmlns:ns1="urn:n" id="x1" e2="5"><items><item n="1">one</item><item n="2">two</item></items>hello<!-- c --><when>2026-01-02T03:04:05Z</when><ptr n="3"/><e1>e</e1></ns1:root>`)
}

func TestTopLevel(t *testing.T) {
	for _, tt := range []struct {
		in   any
		want string
	}{
		{item{N: 1, Val: "v"}, `<item n="1">v</item>`},
		{&item{N: 1}, `<item n="1"/>`},
		{[]item{{N: 1}, {N: 2}}, `<item n="1"/><item n="2"/>`},
		{struct {
			XMLName struct{} `xml:"anon"`
			A       int      `xml:"a"`
		}{A: 1}, `<anon><a>1</a></anon>`},
		{dynName{XMLName: Name{Local: "dyn"}}, `<dyn/>`},
		{dynName{}, `<dynName/>`},
	} {
		b, err := Marshal(tt.in, xmltext.AllowFragments(true))
		if err != nil {
			t.Errorf("Marshal(%#v): %v", tt.in, err)
			continue
		}
		if string(b) != tt.want {
			t.Errorf("Marshal(%#v):\n got %s\nwant %s", tt.in, b, tt.want)
		}
	}
	if b, err := Marshal(42); err != nil || string(b) != "<int>42</int>" {
		t.Errorf("Marshal(42) = %s, %v", b, err)
	}
	if b, err := Marshal([]int{1}); err != nil || string(b) != "<int>1</int>" {
		t.Errorf("Marshal([]int{1}) = %s, %v", b, err)
	}
	if _, err := Marshal([]struct{}{{}}); err == nil {
		t.Errorf("Marshal([]struct{}{{}}): no error for unnamed items")
	}
	if _, err := Marshal(nil); err == nil {
		t.Errorf("Marshal(nil): no error")
	}
}

type dynName struct {
	XMLName Name
}

func TestUnmarshalSkipsAndErrors(t *testing.T) {
	type T struct {
		XMLName Name   `xml:"t"`
		A       string `xml:"a"`
	}
	var v T
	in := `<?xml version="1.0"?><!-- pre --><t x="1"><z><a>no</a></z><a>yes</a><b/></t><!-- post -->`
	if err := Unmarshal([]byte(in), &v); err != nil || v.A != "yes" {
		t.Fatalf("Unmarshal = %+v, %v", v, err)
	}
	err := Unmarshal([]byte(in), &v, RejectUnknownNames(true))
	if !errors.Is(err, ErrUnknownName) {
		t.Errorf("RejectUnknownNames: %v", err)
	}
	var se *SemanticError
	if !errors.As(err, &se) || se.Name.Local != "x" {
		t.Errorf("RejectUnknownNames: error %#v", err)
	}
	if err := Unmarshal([]byte(`<u/>`), &v); err == nil || !strings.Contains(err.Error(), "expected element type <t> but have <u>") {
		t.Errorf("wrong root: %v", err)
	}
	if err := Unmarshal([]byte(``), &v); err == nil {
		t.Errorf("empty: %v", err)
	}
	if err := Unmarshal([]byte(`<t>`), &v); err == nil {
		t.Errorf("truncated: no error")
	}
	if err := Unmarshal([]byte(`<t/>`), v); err == nil {
		t.Errorf("non-pointer: no error")
	}
	var n struct {
		I int `xml:"i"`
	}
	if err := Unmarshal([]byte(`<x><i>z</i></x>`), &n); err == nil || !strings.Contains(err.Error(), "int") {
		t.Errorf("bad int: %v", err)
	}
	if err := Unmarshal([]byte(`<x><i> 12 </i></x>`), &n); err != nil || n.I != 12 {
		t.Errorf("spaced int: %d, %v", n.I, err)
	}
}

func TestInnerXMLAndAny(t *testing.T) {
	type T struct {
		Inner string         `xml:",innerxml"`
		Attrs []xmltext.Attr `xml:",any,attr"`
		Other *item          `xml:",any"`
	}
	var v T
	in := `<t a="1" b="2"><x n="5">five</x><!--c--></t>`
	if err := Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	if v.Inner != `<x n="5">five</x><!--c-->` {
		t.Errorf("innerxml = %q", v.Inner)
	}
	if len(v.Attrs) != 2 || v.Attrs[1].Name.Local != "b" || v.Attrs[1].Value != "2" {
		t.Errorf("any,attr = %+v", v.Attrs)
	}
	if v.Other == nil || v.Other.N != 5 || v.Other.Val != "five" {
		t.Errorf("any = %+v", v.Other)
	}
	b, err := Marshal(struct {
		XMLName struct{} `xml:"t"`
		T
	}{T: T{Inner: "<raw/>", Attrs: []xmltext.Attr{{Name: Name{Local: "q"}, Value: "1"}}}})
	if err != nil || string(b) != `<t q="1"><raw/></t>` {
		t.Errorf("Marshal = %s, %v", b, err)
	}
}

func TestOmit(t *testing.T) {
	type T struct {
		XMLName Name   `xml:"t"`
		E       string `xml:"e,omitempty"`
		Z       zeroer `xml:"z,omitzero"`
		A       int    `xml:"a,attr,omitempty"`
		P       *int   `xml:"p"`
		N       int    `xml:"n"`
	}
	b, err := Marshal(T{Z: zeroer{v: 1}})
	if err != nil || string(b) != `<t><n>0</n></t>` {
		t.Errorf("Marshal = %s, %v", b, err)
	}
	b, err = Marshal(T{}, OmitZeroFields(true))
	if err != nil || string(b) != `<t/>` {
		t.Errorf("OmitZeroFields: Marshal = %s, %v", b, err)
	}
}

type zeroer struct{ v int }

func (z zeroer) IsZero() bool { return z.v <= 1 }

// counter has MarshalerTo and UnmarshalerFrom on its pointer.
type counter struct {
	n    int
	kids []string
}

func (c *counter) MarshalXMLTo(enc *xmltext.Encoder) error {
	enc.Attr(Name{Local: "n"}, strconv.Itoa(c.n))
	for _, k := range c.kids {
		if err := MarshalEncode(enc, &kid{V: k}); err != nil {
			return err
		}
	}
	return nil
}

func (c *counter) UnmarshalXMLFrom(dec *xmltext.Decoder, start xmltext.Token) error {
	for it := start.Attrs(); it.Next(); {
		if it.Name().Local == "n" {
			c.n, _ = strconv.Atoi(string(it.Value()))
		}
	}
	for {
		var k kid
		switch err := UnmarshalDecode(dec, &k); err {
		case nil:
			c.kids = append(c.kids, k.V)
		case io.EOF: // our end tag is next
			_, err := dec.ReadToken()
			return err
		default:
			return err
		}
	}
}

type kid struct {
	XMLName struct{} `xml:"kid"`
	V       string   `xml:",chardata"`
}

func TestMethods(t *testing.T) {
	type T struct {
		XMLName Name     `xml:"t"`
		C       counter  `xml:"c"`
		CP      *counter `xml:"cp"`
	}
	in := T{XMLName: Name{Local: "t"}, C: counter{2, []string{"a", "b"}}}
	roundtrip(t, in, `<t><c n="2"><kid>a</kid><kid>b</kid></c></t>`)
}

type unbalanced struct{}

func (unbalanced) MarshalXMLTo(enc *xmltext.Encoder) error {
	return enc.BeginElement(Name{Local: "x"})
}

func TestMethodErrors(t *testing.T) {
	_, err := Marshal(unbalanced{})
	if !errors.Is(err, errUnbalanced) {
		t.Errorf("unbalanced: %v", err)
	}
}

type celsius float64

func TestFuncs(t *testing.T) {
	type T struct {
		XMLName Name    `xml:"t"`
		Temp    celsius `xml:"temp"`
		Attr    celsius `xml:"a,attr"`
		C       counter `xml:"c"`
	}
	ms := JoinMarshalers(
		MarshalFunc(func(c celsius) ([]byte, error) {
			return fmt.Appendf(nil, "%.1fC", float64(c)), nil
		}),
		MarshalToFunc(func(enc *xmltext.Encoder, c *counter) error {
			if c.n == 0 {
				return SkipFunc
			}
			return enc.Text("count " + strconv.Itoa(c.n))
		}),
	)
	us := JoinUnmarshalers(
		UnmarshalFunc(func(b []byte, c *celsius) error {
			f, err := strconv.ParseFloat(strings.TrimSuffix(string(b), "C"), 64)
			*c = celsius(f)
			return err
		}),
	)
	in := T{XMLName: Name{Local: "t"}, Temp: 21.5, Attr: -1, C: counter{n: 3}}
	b, err := Marshal(&in, WithMarshalers(ms))
	if err != nil {
		t.Fatal(err)
	}
	if want := `<t a="-1.0C"><temp>21.5C</temp><c>count 3</c></t>`; string(b) != want {
		t.Fatalf("got %s\nwant %s", b, want)
	}
	var out T
	if err := Unmarshal(b, &out, WithUnmarshalers(us)); err != nil {
		t.Fatal(err)
	}
	if out.Temp != 21.5 || out.Attr != -1 {
		t.Errorf("Unmarshal = %+v", out)
	}
	// SkipFunc falls back to the method.
	b, err = Marshal(&T{C: counter{kids: []string{"k"}}}, WithMarshalers(ms))
	if err != nil || !bytes.Contains(b, []byte(`<c n="0"><kid>k</kid></c>`)) {
		t.Errorf("SkipFunc: %s, %v", b, err)
	}
}

func TestUnmarshalDecodeStream(t *testing.T) {
	dec := xmltext.NewDecoder(strings.NewReader(`<item n="1">a</item> <item n="2">b</item>`), xmltext.AllowFragments(true))
	var got []item
	for {
		var it item
		err := UnmarshalDecode(dec, &it)
		if err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		got = append(got, it)
	}
	if want := []item{{1, "a"}, {2, "b"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func TestMarshalWrite(t *testing.T) {
	var b strings.Builder
	if err := MarshalWrite(&b, item{N: 1}, xmltext.WithIndent("", "  ")); err != nil {
		t.Fatal(err)
	}
	if b.String() != `<item n="1"/>` {
		t.Errorf("got %q", b.String())
	}
	var v item
	if err := UnmarshalRead(strings.NewReader(b.String()), &v); err != nil || v.N != 1 {
		t.Errorf("UnmarshalRead = %+v, %v", v, err)
	}
}

type benchDoc struct {
	XMLName Name       `xml:"feed"`
	Title   string     `xml:"title"`
	ID      string     `xml:"id"`
	Updated string     `xml:"updated"`
	Entries []benchEnt `xml:"entry"`
}

type benchEnt struct {
	ID      string  `xml:"id,attr"`
	Title   string  `xml:"title"`
	Score   float64 `xml:"score"`
	Count   int     `xml:"count"`
	Summary string  `xml:"summary"`
}

func makeBenchDoc() *benchDoc {
	d := &benchDoc{Title: "t", ID: "urn:x", Updated: "2026"}
	for i := range 20 {
		d.Entries = append(d.Entries, benchEnt{ID: strconv.Itoa(i), Title: "entry", Score: 1.5, Count: i, Summary: "some summary text"})
	}
	return d
}

func TestAllocations(t *testing.T) {
	testenv.SkipIfOptimizationOff(t)
	if race.Enabled {
		t.Skip("skipping malloc count under race detector: sync.Pool drops items")
	}
	d := makeBenchDoc()
	Marshal(d)
	if n := testing.AllocsPerRun(20, func() { Marshal(d) }); n > 1 {
		t.Errorf("Marshal: %v allocs, want <= 1", n)
	}
	b, _ := Marshal(d)
	var out benchDoc
	Unmarshal(b, &out)
	// The strings of the fields, plus growing the slice is reused.
	if n := testing.AllocsPerRun(20, func() {
		out.Entries = out.Entries[:0]
		Unmarshal(b, &out)
	}); n > 3*20+3+2 {
		t.Errorf("Unmarshal: %v allocs", n)
	}
}

func BenchmarkMarshal(b *testing.B) {
	d := makeBenchDoc()
	b.ReportAllocs()
	for b.Loop() {
		Marshal(d)
	}
}

func BenchmarkUnmarshal(b *testing.B) {
	in, _ := Marshal(makeBenchDoc())
	b.ReportAllocs()
	b.SetBytes(int64(len(in)))
	for b.Loop() {
		var out benchDoc
		Unmarshal(in, &out)
	}
}

func TestNestedInnerXML(t *testing.T) {
	type inner struct {
		Raw string `xml:",innerxml"`
		B   string `xml:"b"`
	}
	type outer struct {
		Raw string `xml:",innerxml"`
		In  inner  `xml:"in"`
		C   string `xml:"c"`
	}
	in := `<o><in><b>x</b><i/></in><c>y</c></o>`
	var v outer
	if err := Unmarshal([]byte(in), &v); err != nil {
		t.Fatal(err)
	}
	want := outer{Raw: `<in><b>x</b><i/></in><c>y</c>`, In: inner{Raw: `<b>x</b><i/>`, B: "x"}, C: "y"}
	if v != want {
		t.Errorf("got  %+v\nwant %+v", v, want)
	}
}

type recTree struct {
	Name string    `xml:"name,attr"`
	Kids []recTree `xml:"node"`
	Next *recTree  `xml:"next"`
	Alt  *recAlt   `xml:"alt"`
}

type recAlt struct {
	Back *recTree `xml:"back"`
}

// Recursive types, directly, through a slice, a pointer and another
// struct, round-trip; their arshalers are looked up once per field.
func TestRecursiveTypes(t *testing.T) {
	in := recTree{
		Name: "a",
		Kids: []recTree{{Name: "b"}, {Name: "c", Kids: []recTree{{Name: "d"}}}},
		Next: &recTree{Name: "e"},
		Alt:  &recAlt{Back: &recTree{Name: "f"}},
	}
	b, err := Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out recTree
	if err := Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(in, out) {
		t.Errorf("round trip of %s:\ngot  %+v\nwant %+v", b, out, in)
	}
}
