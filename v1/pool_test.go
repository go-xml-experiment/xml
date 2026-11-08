// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

// poolUpper unmarshals an element by reading its tokens, as a hand-written
// UnmarshalXML does, and keeps an upper-cased copy of the text.
type poolUpper struct{ Text string }

func (u *poolUpper) UnmarshalXML(d *Decoder, start StartElement) error {
	for {
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch t := tok.(type) {
		case CharData:
			u.Text += strings.ToUpper(string(t))
		case EndElement:
			return nil
		}
	}
}

// poolDelegate unmarshals through DecodeElement into a plain struct.
type poolDelegate struct{ V poolPlain }

type poolPlain struct {
	A string `xml:"a,attr"`
	B []byte `xml:"b"`
}

func (p *poolDelegate) UnmarshalXML(d *Decoder, start StartElement) error {
	return d.DecodeElement(&p.V, &start)
}

type poolDoc struct {
	XMLName  Name         `xml:"doc"`
	ID       string       `xml:"id,attr"`
	Raw      []byte       `xml:"rawattr,attr"`
	Inner    []byte       `xml:",innerxml"`
	Data     []byte       `xml:",chardata"`
	Comment  []byte       `xml:",comment"`
	Items    []poolItem   `xml:"item"`
	Upper    poolUpper    `xml:"upper"`
	Delegate poolDelegate `xml:"delegate"`
	Any      []poolAny    `xml:",any"`
}

type poolAny struct {
	XMLName Name
	Inner   string `xml:",innerxml"`
}

type poolItem struct {
	Name  string   `xml:"name"`
	Bytes []byte   `xml:"bytes"`
	Tags  []string `xml:"tags>tag"`
	Inner []byte   `xml:",innerxml"`
}

// poolDocFor builds the n-th test document. Documents differ in length and in
// content, use namespaces for some n, and put the same fields in different
// places so that a buffer shared between two calls would show.
func poolDocFor(n int) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, `<doc id="id%[1]d" rawattr="raw%[1]d"`, n)
	if n%3 == 0 {
		fmt.Fprintf(&sb, ` xmlns:p="urn:p%d" xmlns="urn:d%d"`, n, n)
	}
	sb.WriteString(`>`)
	fmt.Fprintf(&sb, "<!--c%d-->text%d", n, n)
	for i := 0; i < n%5+1; i++ {
		fmt.Fprintf(&sb, `<item><name>n%d.%d</name><bytes>b%d.%d&amp;</bytes><tags><tag>t%d</tag><tag>u%d</tag></tags></item>`, n, i, n, i, i, n)
	}
	fmt.Fprintf(&sb, `<upper>up%d</upper><delegate a="da%d"><b>db%d</b></delegate>`, n, n, n)
	if n%2 == 0 {
		fmt.Fprintf(&sb, `<extra%d k="v">any%d</extra%d>`, n, n, n)
	}
	sb.WriteString(`tail</doc>`)
	return sb.String()
}

// poolDocFresh decodes doc without the pooled state: a Decoder of NewDecoder.
func poolDocFresh(t testing.TB, doc string) (poolDoc, error) {
	var v poolDoc
	err := NewDecoder(strings.NewReader(doc)).Decode(&v)
	return v, err
}

func TestUnmarshalConcurrent(t *testing.T) {
	const docs = 40
	want := make([]poolDoc, docs)
	src := make([]string, docs)
	for n := range want {
		src[n] = poolDocFor(n)
		var err error
		if want[n], err = poolDocFresh(t, src[n]); err != nil {
			t.Fatal(n, err)
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				n := (g*7 + i) % docs
				var v poolDoc
				// hand each call its own copy of the input: the call must not
				// need the input to stay unchanged afterwards
				in := []byte(src[n])
				if err := Unmarshal(in, &v); err != nil {
					t.Errorf("doc %d: %v", n, err)
					return
				}
				for j := range in {
					in[j] = '!'
				}
				if !reflect.DeepEqual(v, want[n]) {
					t.Errorf("doc %d: concurrent Unmarshal\n got %+v\nwant %+v", n, v, want[n])
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestUnmarshalNoAliasing checks that a result of Unmarshal does not change when
// another document is unmarshaled afterwards: no []byte, string or innerxml
// field shares memory with the pooled state or the input.
func TestUnmarshalNoAliasing(t *testing.T) {
	for n := 0; n < 12; n++ {
		in := []byte(poolDocFor(n))
		var first poolDoc
		if err := Unmarshal(in, &first); err != nil {
			t.Fatal(err)
		}
		snapshot := fmt.Sprintf("%+v", first)
		for i := range in {
			in[i] = 'Z'
		}
		for m := 100; m < 130; m++ {
			var other poolDoc
			if err := Unmarshal([]byte(poolDocFor(m)), &other); err != nil {
				t.Fatal(err)
			}
			if err := Unmarshal([]byte(`<doc>`+strings.Repeat("x", 5*m)+`</doc>`), &other); err != nil {
				t.Fatal(err)
			}
		}
		runtime.GC()
		if got := fmt.Sprintf("%+v", first); got != snapshot {
			t.Fatalf("doc %d changed after later calls:\n got %s\nwant %s", n, got, snapshot)
		}
	}
}

func TestMarshalConcurrent(t *testing.T) {
	const docs = 40
	vals := make([]poolDoc, docs)
	want := make([][]byte, docs)
	wantIndent := make([][]byte, docs)
	for n := range vals {
		vals[n], _ = poolDocFresh(t, poolDocFor(n))
		for i, indent := range []bool{false, true} {
			var b bytes.Buffer
			enc := NewEncoder(&b)
			if indent {
				enc.Indent("> ", "  ")
			}
			if err := enc.Encode(vals[n]); err != nil {
				t.Fatal(err)
			}
			enc.Close()
			if i == 0 {
				want[n] = b.Bytes()
			} else {
				wantIndent[n] = b.Bytes()
			}
		}
	}
	var wg sync.WaitGroup
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				n := (g*11 + i) % docs
				got, err := Marshal(vals[n])
				if err != nil {
					t.Errorf("doc %d: %v", n, err)
					return
				}
				if !bytes.Equal(got, want[n]) {
					t.Errorf("doc %d: concurrent Marshal\n got %s\nwant %s", n, got, want[n])
					return
				}
				ind, err := MarshalIndent(vals[n], "> ", "  ")
				if err != nil || !bytes.Equal(ind, wantIndent[n]) {
					t.Errorf("doc %d: MarshalIndent\n got %s\nwant %s (%v)", n, ind, wantIndent[n], err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// TestMarshalNoAliasing checks that the slice Marshal returns is the caller's:
// later calls leave it unchanged, and appending to it does not reach the pool.
func TestMarshalNoAliasing(t *testing.T) {
	var results [][]byte
	var snaps []string
	for n := 0; n < 20; n++ {
		v, _ := poolDocFresh(t, poolDocFor(n))
		b, err := Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, b)
		snaps = append(snaps, string(b))
		b = append(b[:cap(b)], "garbage"...) // writes past len, into the slack
		_ = b
	}
	for n := 20; n < 60; n++ {
		v, _ := poolDocFresh(t, poolDocFor(n))
		if _, err := Marshal(v); err != nil {
			t.Fatal(err)
		}
		if _, err := MarshalIndent(v, "", " "); err != nil {
			t.Fatal(err)
		}
	}
	for n, b := range results {
		if string(b[:len(snaps[n])]) != snaps[n] {
			t.Errorf("result %d changed after later calls: %q, want %q", n, b[:len(snaps[n])], snaps[n])
		}
	}
	// a result over maxPooled is handed over without a copy: later calls must
	// still leave it alone
	for _, n := range []int{maxPooled - 100, maxPooled, maxPooled + 100, 200 << 10} {
		first, err := Marshal(poolBigText{S: strings.Repeat("a", n)})
		if err != nil {
			t.Fatal(err)
		}
		snap := string(first)
		for i := 0; i < 20; i++ {
			Marshal(poolBigText{S: strings.Repeat("z", n)})
			MarshalIndent(poolBigText{S: strings.Repeat("y", n/2)}, "", " ")
			Marshal(poolDoc{ID: "small"})
		}
		if string(first) != snap {
			t.Errorf("result of %d bytes changed after later calls", len(snap))
		}
	}
	if b, err := Marshal(nil); b != nil || err != nil {
		t.Errorf("Marshal(nil) = %q, %v; want nil, nil", b, err)
	}
	var np *poolDoc
	if b, err := Marshal(np); b != nil || err != nil {
		t.Errorf("Marshal(nil pointer) = %q, %v; want nil, nil", b, err)
	}
}

// poolSettings changes every public setting of the Decoder it is handed.
type poolSettings struct{ S string }

func (p *poolSettings) UnmarshalXML(d *Decoder, start StartElement) error {
	d.Strict = false
	d.AutoClose = []string{"br"}
	d.Entity = map[string]string{"foo": "bar"}
	d.CharsetReader = func(string, io.Reader) (io.Reader, error) { return nil, io.EOF }
	d.DefaultSpace = "urn:leaked"
	return d.Skip()
}

// TestUnmarshalSettingsDoNotLeak checks that settings an UnmarshalXML changes
// are gone for the next call.
func TestUnmarshalSettingsDoNotLeak(t *testing.T) {
	type holder struct {
		XMLName Name `xml:"h"`
		P       poolSettings
	}
	for i := 0; i < 20; i++ {
		var h holder
		if err := Unmarshal([]byte(`<h><P>x</P></h>`), &h); err != nil {
			t.Fatal(err)
		}
		// strict again: a mismatched end tag is an error
		var v struct{ A string }
		if err := Unmarshal([]byte(`<a><b>x</a>`), &v); err == nil {
			t.Fatal("unmatched tag accepted: Strict leaked")
		}
		// the entity from the previous call is unknown again
		var s string
		if err := Unmarshal([]byte(`<a>&foo;</a>`), &s); err == nil {
			t.Fatal("entity foo accepted: Entity leaked")
		}
		// no default namespace
		var n struct{ XMLName Name }
		if err := Unmarshal([]byte(`<a/>`), &n); err != nil || n.XMLName.Space != "" {
			t.Fatalf("XMLName = %+v, %v: DefaultSpace leaked", n.XMLName, err)
		}
		// no charset converter
		if err := Unmarshal([]byte(`<?xml version="1.0" encoding="x-foo"?><a/>`), &n); err == nil || !strings.Contains(err.Error(), "CharsetReader is nil") {
			t.Fatalf("encoding declaration = %v: CharsetReader leaked", err)
		}
	}
}

// TestPutDecoderClears checks the state of a Decoder as it goes back to the pool.
func TestPutDecoderClears(t *testing.T) {
	d := getDecoder([]byte(`<a xmlns:p="urn:p"><p:b c="1">text<!--k--></p:b></a>`))
	var v struct {
		B struct {
			C string `xml:"c,attr"`
			T string `xml:",chardata"`
			I string `xml:",innerxml"`
		} `xml:"urn:p b"`
	}
	if err := d.Decode(&v); err != nil {
		t.Fatal(err)
	}
	d.Strict, d.AutoClose, d.Entity = false, []string{"x"}, map[string]string{"a": "b"}
	d.CharsetReader = func(string, io.Reader) (io.Reader, error) { return nil, nil }
	d.DefaultSpace = "urn:x"
	d.next, d.err = pendingToken{k: rawOther, t: CharData("x")}, io.EOF
	d.pushElement(Name{Local: "leftover"})
	d.ns["q"] = "urn:q"
	putDecoder(d)
	fresh := NewDecoder(nil)
	if d.xd != nil || d.spare == nil || d.data != nil || d.src != nil || d.in != nil || d.t != nil {
		t.Errorf("input or tokenizer still attached: xd=%v data=%v src=%v in=%v", d.xd, d.data, d.src, d.in)
	}
	if !d.Strict || d.AutoClose != nil || d.Entity != nil || d.CharsetReader != nil || d.DefaultSpace != "" {
		t.Errorf("public settings not reset: %+v", d)
	}
	if d.next.k != rawNone || d.next.t != nil || d.err != nil || d.stk != nil || d.stkDepth != 0 || len(d.ns) != 0 || d.needClose || d.saving {
		t.Errorf("parse state not reset: %+v", d)
	}
	for s := d.free; s != nil; s = s.next {
		if s.name != (Name{}) || s.kind != 0 || s.ok {
			t.Errorf("free stack record holds %+v", *s)
		}
	}
	_ = fresh
}

// TestPoolDropsBigBuffers checks that a document that needs more than 64 KiB of
// buffer does not leave that buffer in the pool.
func TestPoolDropsBigBuffers(t *testing.T) {
	big := strings.Repeat("a&amp;b", 60<<10) // ~360 KiB of text, scratch needs the decoded text
	deep := strings.Repeat("<n>", 5000) + strings.Repeat("</n>", 5000)
	docs := map[string]string{
		"text":     `<doc>` + big + `</doc>`,
		"attr":     `<doc id="` + big + `" rawattr="` + big + `"/>`,
		"innerxml": `<doc>` + strings.Repeat(`<a>x</a>`, 40000) + `</doc>`,
		"deep":     `<doc>` + deep + `</doc>`,
		"ns":       `<doc>` + strings.Repeat(`<a xmlns:p="u">`, 3000) + strings.Repeat(`</a>`, 3000) + `</doc>`,
	}
	for name, doc := range docs {
		d := getDecoder([]byte(doc))
		var v poolDoc
		if err := d.Decode(&v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n := putDecoder(d); n > maxPooled {
			t.Errorf("%s: Decoder keeps %d bytes of buffers, want at most %d", name, n, maxPooled)
		}
		if d.nsPeak != 0 || len(d.ns) != 0 {
			t.Errorf("%s: namespace map not reset", name)
		}
	}
	for name, v := range map[string]any{
		"text":  poolBigText{S: big},
		"attr":  poolBigAttr{S: big},
		"many":  poolBigMany{S: strings.Split(strings.Repeat("x,", 100000), ",")},
		"bytes": poolBigBytes{B: []byte(big)},
	} {
		st := getEncoder()
		out, err := st.marshal(v)
		if err != nil || len(out) < 100<<10 {
			t.Fatalf("%s: %d bytes, %v", name, len(out), err)
		}
		if n := putEncoder(st); n > maxPooled {
			t.Errorf("%s: Encoder keeps %d bytes of buffers, want at most %d", name, n, maxPooled)
		}
		if st.buf.Cap() > maxPooled {
			t.Errorf("%s: output buffer of %d bytes kept", name, st.buf.Cap())
		}
	}
	// a normal call still works afterwards, and a small one keeps its buffers
	var v poolDoc
	if err := Unmarshal([]byte(poolDocFor(1)), &v); err != nil {
		t.Fatal(err)
	}
	st := getEncoder()
	if _, err := st.marshal(v); err != nil {
		t.Fatal(err)
	}
	if n := putEncoder(st); n == 0 || n > maxPooled {
		t.Errorf("small document: Encoder keeps %d bytes, want some, at most %d", n, maxPooled)
	}
}

// TestPoolDoesNotPinCallerData checks, with the garbage collector, that nothing
// the caller passed to Unmarshal or Marshal stays reachable from the pools.
func TestPoolDoesNotPinCallerData(t *testing.T) {
	type payload struct {
		_ [64]byte
		S string `xml:"s"`
		B []byte `xml:"b"`
	}
	type holder struct {
		XMLName Name
		A       string   `xml:"a,attr"`
		P       *payload `xml:"p"`
	}
	check := func(name string, run func(collected chan<- string), want ...string) {
		collected := make(chan string, 8)
		run(collected)
		// One collection only: a sync.Pool keeps what it holds through the
		// first one (the victim cache) and drops it in the second, which would
		// hide a reference that the state failed to clear.
		runtime.GC()
		got := map[string]bool{}
		for deadline := time.Now().Add(2 * time.Second); len(got) < len(want) && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
			for len(collected) > 0 {
				got[<-collected] = true
			}
		}
		for _, what := range want {
			if !got[what] {
				t.Errorf("%s: the %s is still reachable after the call returned", name, what)
			}
		}
	}
	check("Unmarshal", func(collected chan<- string) {
		in := []byte(`<h a="x"><p><s>hello</s><b>world</b></p></h>`)
		runtime.AddCleanup(&in[0], func(string) { collected <- "input" }, "")
		out := new(holder)
		runtime.AddCleanup(out, func(string) { collected <- "output value" }, "")
		if err := Unmarshal(in, out); err != nil {
			t.Error(err)
		}
		if out.P == nil || out.P.S != "hello" {
			t.Errorf("Unmarshal = %+v", out.P)
		}
	}, "input", "output value")
	for _, m := range []string{"Marshal", "MarshalIndent"} {
		check(m, func(collected chan<- string) {
			// element, attribute and namespace names built at run time end up in
			// the encoder's tables
			mem := []byte(strings.Repeat("n", 40))
			str := unsafe.String(&mem[0], len(mem))
			runtime.AddCleanup(&mem[0], func(string) { collected <- "namespaced names" }, "")
			mem2 := []byte(strings.Repeat("m", 40))
			str2 := unsafe.String(&mem2[0], len(mem2))
			runtime.AddCleanup(&mem2[0], func(string) { collected <- "plain names" }, "")
			vs := []*holder{
				{XMLName: Name{Space: str, Local: str[1:]}, A: str[2:], P: new(payload)},
				{XMLName: Name{Local: str2}, A: str2[2:], P: new(payload)},
			}
			runtime.AddCleanup(vs[1], func(string) { collected <- "value" }, "")
			for _, v := range vs {
				var err error
				if m == "Marshal" {
					_, err = Marshal(v)
				} else {
					_, err = MarshalIndent(v, "", " ")
				}
				if err != nil {
					t.Error(err)
				}
			}
		}, "namespaced names", "plain names", "value")
	}
}

type poolBigText struct{ S string }
type poolBigAttr struct {
	S string `xml:"s,attr"`
}
type poolBigMany struct{ S []string }
type poolBigBytes struct{ B []byte }

// poolPanic panics in UnmarshalXML.
type poolPanic struct{}

func (poolPanic) UnmarshalXML(*Decoder, StartElement) error { panic("boom") }

// poolNested calls Unmarshal from inside UnmarshalXML, on the same goroutine.
type poolNested struct{ N poolPlain }

func (p *poolNested) UnmarshalXML(d *Decoder, start StartElement) error {
	var inner poolDoc
	if err := Unmarshal([]byte(poolDocFor(4)), &inner); err != nil {
		return err
	}
	if inner.ID != "id4" {
		return fmt.Errorf("nested Unmarshal: ID = %q", inner.ID)
	}
	if err := d.DecodeElement(&p.N, &start); err != nil {
		return err
	}
	b, err := Marshal(inner)
	if err != nil || len(b) == 0 {
		return fmt.Errorf("nested Marshal: %v", err)
	}
	return nil
}

func TestUnmarshalPanicAndReentry(t *testing.T) {
	for i := 0; i < 5; i++ {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			var v struct{ P poolPanic }
			Unmarshal([]byte(`<a><P/></a>`), &v)
		}()
		var v struct{ N poolNested }
		if err := Unmarshal([]byte(`<a><N a="outer"><b>bb</b></N></a>`), &v); err != nil {
			t.Fatal(err)
		}
		if v.N.N.A != "outer" || string(v.N.N.B) != "bb" {
			t.Fatalf("outer result damaged by nested calls: %+v", v.N.N)
		}
	}
}

type poolRetainedDecoder struct {
	decoder *Decoder
	text    CharData
	err     error
}

func (v *poolRetainedDecoder) UnmarshalXML(d *Decoder, start StartElement) error {
	v.decoder = d
	d.Entity = map[string]string{"saved": "retained"}
	if v.err != nil {
		token, err := d.Token()
		if err != nil {
			return err
		}
		v.text = token.(CharData)
		return v.err
	}
	return d.Skip()
}

func TestUnmarshalRetainedDecoder(t *testing.T) {
	for _, hookErr := range []error{nil, io.ErrUnexpectedEOF} {
		v := poolRetainedDecoder{err: hookErr}
		if err := Unmarshal([]byte(`<r>kept &amp; text</r><tail/>`), &v); err != hookErr {
			t.Fatalf("Unmarshal = %v, want %v", err, hookErr)
		}
		for range 3 {
			var plain string
			if err := Unmarshal([]byte(`<other>overwritten &amp; text</other>`), &plain); err != nil {
				t.Fatal(err)
			}
		}
		if v.decoder.Entity["saved"] != "retained" {
			t.Fatal("retained decoder settings were reset")
		}
		if hookErr != nil {
			if string(v.text) != "kept & text" {
				t.Fatalf("last token was changed without another Token call: %q", v.text)
			}
			if token, err := v.decoder.Token(); err != nil || token != (EndElement{Name{Local: "r"}}) {
				t.Fatalf("resume after hook error: %v, %v", token, err)
			}
			// An incomplete hook leaves its EOF marker, as in the legacy decoder.
			if token, err := v.decoder.Token(); token != nil || err != io.EOF {
				t.Fatalf("hook boundary after error: %v, %v", token, err)
			}
			continue
		}
		token, err := v.decoder.Token()
		start, ok := token.(StartElement)
		if err != nil || !ok || start.Name != (Name{Local: "tail"}) {
			t.Fatalf("resume after Unmarshal: %v, %v", token, err)
		}
	}
}

type poolRetainedEncoder struct {
	encoder *Encoder
	err     error
}

func (v *poolRetainedEncoder) MarshalXML(e *Encoder, start StartElement) error {
	v.encoder = e
	if err := e.EncodeElement("kept", start); err != nil {
		return err
	}
	return v.err
}

func TestMarshalRetainedEncoder(t *testing.T) {
	for _, indent := range []bool{false, true} {
		for _, hookErr := range []error{nil, io.ErrUnexpectedEOF} {
			v := poolRetainedEncoder{err: hookErr}
			var err error
			if indent {
				_, err = MarshalIndent(&v, "", "  ")
			} else {
				_, err = Marshal(&v)
			}
			if err != hookErr {
				t.Fatalf("Marshal = %v, want %v", err, hookErr)
			}
			for range 3 {
				if _, err := Marshal(poolPlain{A: "other"}); err != nil {
					t.Fatal(err)
				}
			}
			err = v.encoder.EncodeToken(CharData("continued"))
			if hookErr == nil {
				// Marshal closes its encoder on success, just like the legacy API.
				if err == nil || err.Error() != "use of closed Encoder" {
					t.Fatalf("retained closed encoder: %v", err)
				}
			} else if err != nil {
				t.Fatalf("continue after hook error: %v", err)
			} else if err := v.encoder.Flush(); err != nil {
				t.Fatalf("flush after hook error: %v", err)
			}
		}
	}
}

// The output buffer follows the jsontext policy: a buffer above 4 KiB that
// is under a quarter full four times in a row is dropped, and the next one
// is sized after the last output.
func TestPoolBufferUtilization(t *testing.T) {
	type doc struct {
		XMLName Name   `xml:"d"`
		S       string `xml:"s"`
	}
	big := doc{S: strings.Repeat("x", 32<<10)}
	small := doc{S: "x"}
	st := getEncoder()
	if _, err := st.marshal(big); err != nil {
		t.Fatal(err)
	}
	putEncoder(st)
	bigCap := st.buf.Cap()
	if bigCap < 32<<10 {
		t.Fatalf("buffer of %d bytes after a 32 KiB output", bigCap)
	}
	for i := range 5 {
		// st is reused directly so that the pool cannot hand out another.
		st.reset()
		if _, err := st.marshal(small); err != nil {
			t.Fatal(err)
		}
		putEncoder(st)
		kept := st.buf.Cap() == bigCap
		if want := i < 4; kept != want {
			t.Fatalf("small output %d: buffer kept = %v, want %v", i+1, kept, want)
		}
	}
	if st.bufStats.prevLen == 0 || st.bufStats.prevLen > 64 {
		t.Errorf("prevLen = %d, want the length of the small output", st.bufStats.prevLen)
	}
}
