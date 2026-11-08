// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

var (
	// "xmlns" is an ordinary URI; XMLNSNamespace names declarations, so as an element
	// namespace it must be refused and as an attribute namespace it declares.
	fzSpaces = []string{"", "urn:a", "urn:b", "urn:c", XMLNamespace, "xmlns", XMLNSNamespace}
	fzLocals = []string{"a", "b", "c", "x", "ns1"}
	fzPrefix = []string{"p", "q", "ns1", "ns2", "xml", "xmlns"}
	fzValues = []string{"", "urn:a", "urn:b", "urn:c", "urn:d", "v", XMLNamespace, "xmlns", XMLNSNamespace}
)

// fzElem is what the call sequence asked for, to compare with the parse.
type fzElem struct {
	name  Name
	attrs map[Name]string
	decls map[Name]string
}

// driveEncoder runs a call sequence derived from data against e. It reports
// the calls made, the elements started and whether every call (and the final
// Close) succeeded. wide selects the larger name pools.
func driveEncoder(e *Encoder, data []byte, wide bool) (calls []string, elems []*fzElem, ok bool) {
	next := func() int {
		if len(data) == 0 {
			return 0
		}
		b := data[0]
		data = data[1:]
		return int(b)
	}
	spaces, prefixes, values := fzSpaces, fzPrefix, fzValues
	if wide {
		spaces, prefixes, values = fzWide.spaces, fzWide.prefixes, fzWide.values
	}
	pick := func(s []string) string { return s[next()%len(s)] }
	var open []*fzElem
	record := func(format string, a ...any) { calls = append(calls, fmt.Sprintf(format, a...)) }
	limit, maxDepth := 64, 6
	if wide {
		limit, maxDepth = 400, 12
	}
	for len(data) > 0 && len(calls) < limit {
		var err error
		switch op := next() % 8; {
		case op <= 1 && len(open) < maxDepth:
			n := Name{Space: pick(spaces), Local: pick(fzLocals)}
			record("Begin(%q,%q)", n.Space, n.Local)
			err = e.BeginElement(n)
			el := &fzElem{name: n, attrs: map[Name]string{}, decls: map[Name]string{}}
			open = append(open, el)
			elems = append(elems, el)
		case op <= 4 && len(open) > 0:
			var n Name
			switch k := next() % 4; k {
			case 0:
				n = Name{Space: XMLNSNamespace, Local: pick(prefixes)}
			case 1:
				n = Name{Space: XMLNSNamespace, Local: "xmlns"}
			default:
				n = Name{Space: pick(spaces), Local: pick(fzLocals)}
			}
			v := pick(values)
			record("Attr(%q,%q,%q)", n.Space, n.Local, v)
			err = e.Attr(n, v)
			if err == nil {
				if el := open[len(open)-1]; isDeclName(n) {
					el.decls[n] = v
				} else {
					el.attrs[n] = v
				}
			}
		case op == 5 && len(open) > 0:
			record("End")
			err = e.EndElement()
			open = open[:len(open)-1]
		case op == 6 && len(open) > 0:
			record("Text")
			err = e.Text("t")
		}
		if err != nil {
			return calls, elems, false
		}
	}
	for ; len(open) > 0; open = open[:len(open)-1] {
		record("End")
		if err := e.EndElement(); err != nil {
			return calls, elems, false
		}
	}
	return calls, elems, e.Close() == nil
}

var fzWide struct{ spaces, prefixes, values []string }

func init() {
	fzWide.spaces = []string{"", XMLNamespace, "xmlns", XMLNSNamespace}
	fzWide.prefixes = []string{"p", "xml", "xmlns"}
	fzWide.values = []string{"", "v", XMLNamespace, "xmlns", XMLNSNamespace}
	for i := range 24 {
		fzWide.spaces = append(fzWide.spaces, fmt.Sprintf("urn:u%d", i))
		fzWide.prefixes = append(fzWide.prefixes, fmt.Sprintf("p%d", i), fmt.Sprintf("ns%d", i+1))
		fzWide.values = append(fzWide.values, fmt.Sprintf("urn:u%d", i))
	}
}

// checkEncoderNamespaces: whenever the Encoder accepts a sequence, its output
// parses and says what was asked for; the indexed namespace lookups give the
// same bytes as plain scans.
func checkEncoderNamespaces(t testing.TB, data []byte) {
	wide := len(data) > 0 && data[0]&1 == 1
	var b bytes.Buffer
	e := NewEncoder(&b, BufferSize(16))
	calls, elems, ok := driveEncoder(e, data, wide)
	if wide {
		saved := encNSMin
		encNSMin = 1 << 30
		var plain bytes.Buffer
		_, _, pok := driveEncoder(NewEncoder(&plain, BufferSize(16)), data, wide)
		encNSMin = saved
		if pok != ok || plain.String() != b.String() {
			t.Fatalf("indexed lookups differ from scans for %s\n  indexed %q (%v)\n  scanned %q (%v)", strings.Join(calls, " "), b.String(), ok, plain.String(), pok)
		}
	}
	if !ok || b.Len() == 0 {
		return
	}
	fail := func(format string, a ...any) {
		t.Helper()
		t.Fatalf("accepted %s\noutput %q: %s", strings.Join(calls, " "), b.String(), fmt.Sprintf(format, a...))
	}
	d := NewDecoderBytes(b.Bytes())
	n := 0
	for {
		tk, err := d.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail("does not parse: %v", err)
		}
		if tk.Kind() != StartElement {
			continue
		}
		if n >= len(elems) {
			fail("more elements than written")
		}
		want := elems[n]
		n++
		if tk.Name() != want.name {
			fail("element %d is %v, want %v", n, tk.Name(), want.name)
		}
		got := map[Name]string{}
		decls := map[Name]string{}
		for it := tk.Attrs(); it.Next(); {
			if nm := it.Name(); isDeclName(nm) {
				decls[nm] = string(it.Value())
			} else {
				got[nm] = string(it.Value())
			}
		}
		if len(got) != len(want.attrs) {
			fail("element %d attributes %v, want %v", n, got, want.attrs)
		}
		for k, v := range want.attrs {
			if gv, ok := got[k]; !ok || gv != v {
				fail("element %d attributes %v, want %v", n, got, want.attrs)
			}
		}
		for k, v := range want.decls {
			if gv, ok := decls[k]; !ok || gv != v {
				fail("element %d declarations %v, want %v", n, decls, want.decls)
			}
		}
	}
	if n != len(elems) {
		fail("%d elements, want %d", n, len(elems))
	}
}

func FuzzEncoderNamespaces(f *testing.F) {
	f.Add([]byte{0, 0, 2, 1, 0, 2, 0, 0, 3, 1})
	f.Add([]byte{0, 0, 2, 0, 1, 0, 1, 0})
	f.Add([]byte{1, 5, 3, 0, 0, 0, 0, 0, 3, 1, 1, 0, 0, 0})
	f.Add([]byte{1, 0, 3, 5, 0, 7, 2, 3, 1, 2, 3, 1, 2, 3, 2, 1, 2, 0, 3, 5, 0, 3, 9, 2, 1, 4, 3, 2, 0, 7, 1})
	f.Fuzz(func(t *testing.T, data []byte) { checkEncoderNamespaces(t, data) })
}

// The two cases the review found: a declaration written after the encoder
// generated the same prefix, and an explicit default namespace on an element
// without a namespace inside a default-namespace parent.
func TestEncoderRejectsDuplicateDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name string
		run  func(e *Encoder) error
	}{
		{"eager auto prefix then explicit", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Space: "urn:x", Local: "y"}, "1")
			if err := e.Attr(Name{Space: XMLNSNamespace, Local: "ns1"}, "urn:other"); err != nil {
				return err
			}
			return e.EndElement()
		}},
		{"eager explicit twice", func(e *Encoder) error {
			e.BeginElement(Name{Local: "a"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "ns1"}, "urn:x")
			if err := e.Attr(Name{Space: XMLNSNamespace, Local: "ns1"}, "urn:x"); err != nil {
				return err
			}
			return e.EndElement()
		}},
		{"deferred default in default parent", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:p", Local: "r"})
			e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "urn:p")
			e.BeginElement(Name{Local: "c"})
			if err := e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "urn:d"); err != nil {
				return err
			}
			e.EndElement()
			return e.EndElement()
		}},
		{"eager prefix used then redeclared", func(e *Encoder) error {
			e.BeginElement(Name{Space: "urn:c", Local: "x"}) // declares ns1
			e.BeginElement(Name{Local: "x"})
			e.Attr(Name{Space: "urn:c", Local: "a"}, "1") // written as ns1:a
			if err := e.Attr(Name{Space: XMLNSNamespace, Local: "ns1"}, "urn:a"); err != nil {
				return err
			}
			e.EndElement()
			return e.EndElement()
		}},
		{"eager default on unqualified element", func(e *Encoder) error {
			e.BeginElement(Name{Local: "c"})
			if err := e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "urn:d"); err != nil {
				return err
			}
			return e.EndElement()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var b bytes.Buffer
			e := NewEncoder(&b)
			err := tc.run(e)
			if err == nil {
				err = e.Close()
			}
			if err == nil {
				t.Fatalf("accepted: %q", b.String())
			}
		})
	}
}

// Declaring a prefix again with the URI it already has changes nothing.
func TestEncoderAllowsSameRedeclaration(t *testing.T) {
	got := encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Space: "urn:c", Local: "x"})
		e.BeginElement(Name{Local: "x"})
		e.Attr(Name{Space: "urn:c", Local: "a"}, "1")
		e.Attr(Name{Space: XMLNSNamespace, Local: "ns1"}, "urn:c")
		e.EndElement()
		return e.EndElement()
	})
	if want := `<ns1:x xmlns:ns1="urn:c"><x ns1:a="1" xmlns:ns1="urn:c"/></ns1:x>`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// An explicit empty default declaration on an unqualified element is fine
// wherever it stands.
func TestEncoderAllowsEmptyDefaultDeclaration(t *testing.T) {
	got := encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Space: "urn:p", Local: "r"})
		e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "urn:p")
		e.BeginElement(Name{Local: "c"})
		e.Attr(Name{Space: XMLNSNamespace, Local: "xmlns"}, "")
		e.EndElement()
		return e.EndElement()
	})
	if want := `<r xmlns="urn:p"><c xmlns=""/></r>`; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// nsHeavyDoc is <r xmlns:p0="u0" ... xmlns:pN="uN" p0:a="1" ... pN:a="1"/>,
// with the root in a default namespace when qualified (which takes the
// deferred start-tag path).
func nsHeavyDoc(n int, qualified bool) []byte {
	var b strings.Builder
	b.WriteString("<r")
	if qualified {
		b.WriteString(` xmlns="urn:root"`)
	}
	for i := range n {
		fmt.Fprintf(&b, ` xmlns:p%d="u%d"`, i, i)
	}
	for i := range n {
		fmt.Fprintf(&b, ` p%d:a="1"`, i)
	}
	b.WriteString("/>")
	return []byte(b.String())
}

// generatedPrefix declares ns1..nsN explicitly, then needs one generated
// prefix, which has to step past all of them.
func generatedPrefix(t testing.TB, n int, qualified bool) []byte {
	var out bytes.Buffer
	e := NewEncoder(&out)
	space := ""
	if qualified {
		space = "urn:root"
	}
	e.BeginElement(Name{Space: space, Local: "r"})
	for i := 1; i <= n; i++ {
		e.Attr(Name{Space: XMLNSNamespace, Local: "ns" + strconv.Itoa(i)}, "u"+strconv.Itoa(i))
	}
	e.Attr(Name{Space: "urn:fresh", Local: "a"}, "1")
	e.Attr(Name{Space: "urn:fresh2", Local: "a"}, "1")
	e.EndElement()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// reencode decodes data and writes every token to an Encoder.
func reencode(t testing.TB, data []byte, opts ...Options) []byte {
	var out bytes.Buffer
	e := NewEncoder(&out, opts...)
	d := NewDecoderBytes(data, MaxAttributes(0))
	for {
		tk, err := d.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := e.WriteToken(tk); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// Re-encoding a tag with tens of thousands of declarations took 1.7 s for 40k
// before the encoder indexed its bindings (decoding alone 12 ms).
func TestEncoderNamespaceLookupLinear(t *testing.T) {
	const n = 60000
	for _, qualified := range []bool{false, true} {
		t.Run(fmt.Sprintf("reencode/qualified=%v", qualified), func(t *testing.T) {
			data := nsHeavyDoc(n, qualified)
			start := time.Now()
			reencode(t, data)
			if el := time.Since(start); el > 2*time.Second {
				t.Fatalf("%d bytes took %v", len(data), el)
			}
		})
		t.Run(fmt.Sprintf("generated/qualified=%v", qualified), func(t *testing.T) {
			start := time.Now()
			out := generatedPrefix(t, n, qualified)
			if el := time.Since(start); el > 2*time.Second {
				t.Fatalf("%d bytes took %v", len(out), el)
			}
			d := NewDecoderBytes(out, MaxAttributes(0))
			for {
				if _, err := d.ReadToken(); err != nil {
					if err != io.EOF {
						t.Fatal(err)
					}
					break
				}
			}
		})
	}
}

// Indexed and scanned lookups give the same bytes.
func TestEncoderNamespaceIndexMatchesScan(t *testing.T) {
	run := func() (out [4][]byte) {
		for i, qualified := range []bool{false, true} {
			out[i] = reencode(t, nsHeavyDoc(300, qualified))
			out[2+i] = generatedPrefix(t, 300, qualified)
		}
		return
	}
	got := run()
	saved := encNSMin
	encNSMin = 1 << 30
	want := run()
	encNSMin = saved
	for i := range got {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("case %d: indexed output differs from scanned\n%.200s\n%.200s", i, got[i], want[i])
		}
	}
}

func BenchmarkEncoderNamespaces(b *testing.B) {
	for _, n := range []int{1000, 10000, 40000} {
		for _, qualified := range []bool{false, true} {
			name := fmt.Sprintf("N=%d/qualified=%v", n, qualified)
			data := nsHeavyDoc(n, qualified)
			b.Run("reencode/"+name, func(b *testing.B) {
				b.SetBytes(int64(len(data)))
				for range b.N {
					reencode(b, data)
				}
			})
			b.Run("generated/"+name, func(b *testing.B) {
				for range b.N {
					generatedPrefix(b, n, qualified)
				}
			})
		}
	}
}
