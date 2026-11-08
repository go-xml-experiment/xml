// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"fmt"
	"github.com/go-xml-experiment/xml/internal/testenv"
	"github.com/go-xml-experiment/xml/internal/xmlopts"
	"io"
	"strings"
	"testing"
)

// Generated prefixes are scoped to the element that declares them, so
// siblings reuse a number while a nested declaration takes the next one.
func TestEncoderGeneratedPrefixScope(t *testing.T) {
	got := encode(t, nil, func(e *Encoder) error {
		e.BeginElement(Name{Local: "root"})
		e.BeginElement(Name{Space: "urn:a", Local: "x"})
		e.EndElement()
		e.BeginElement(Name{Space: "urn:b", Local: "y"})
		e.BeginElement(Name{Space: "urn:c", Local: "z"})
		e.EndElement()
		e.EndElement()
		return e.EndElement()
	})
	want := `<root><ns1:x xmlns:ns1="urn:a"/><ns1:y xmlns:ns1="urn:b"><ns2:z xmlns:ns2="urn:c"/></ns1:y></root>`
	if got != want {
		t.Fatalf("\n got %s\nwant %s", got, want)
	}
}

// Prefixes past the precomputed table are still distinct and correct.
func TestEncoderGeneratedPrefixDeep(t *testing.T) {
	const depth = len(genPrefixes) + 3
	var b strings.Builder
	e := NewEncoder(&b)
	for i := range depth {
		e.BeginElement(Name{Space: fmt.Sprintf("urn:%d", i), Local: "e"})
	}
	for range depth {
		e.EndElement()
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	d := NewDecoderBytes([]byte(b.String()))
	for i := range depth {
		tok, err := d.ReadToken()
		if err != nil {
			t.Fatal(err)
		}
		if n := tok.Name(); n.Space != fmt.Sprintf("urn:%d", i) {
			t.Fatalf("element %d: space %q", i, n.Space)
		}
	}
}

func TestEncoderNamespacedSiblingsNoAllocations(t *testing.T) {
	testenv.SkipIfOptimizationOff(t)
	e := NewEncoder(io.Discard)
	e.BeginElement(Name{Local: "root"})
	sibling := func() {
		e.BeginElement(Name{Space: "urn:a", Local: "row"})
		e.Attr(Name{Space: "urn:b", Local: "id"}, "1")
		e.EndElement()
	}
	sibling() // warm up buffers
	if n := testing.AllocsPerRun(100, sibling); n != 0 {
		t.Errorf("namespaced sibling: %v allocs, want 0", n)
	}
}

// Names are right with and without a string cache, and with one set
// (as encoding/xml does while unmarshaling) repeated names do not allocate.
func TestStringCache(t *testing.T) {
	var b strings.Builder
	b.WriteString("<r>")
	for i := range 2000 {
		fmt.Fprintf(&b, "<n%d/>", i%600)
	}
	b.WriteString("</r>")
	doc := []byte(b.String())
	for _, cached := range []bool{false, true} {
		d := NewDecoderBytes(doc)
		if cached {
			*Internal.Export(&xmlopts.AllowInternalUse).StringCache(d) = new([256]string)
		}
		d.ReadToken() // <r>
		for i := range 2000 {
			tok, err := d.ReadToken()
			if err != nil {
				t.Fatal(err)
			}
			if got, want := tok.Name().Local, fmt.Sprintf("n%d", i%600); got != want {
				t.Fatalf("cached=%v, element %d: %q, want %q", cached, i, got, want)
			}
			d.ReadToken() // end
		}
	}

	testenv.SkipIfOptimizationOff(t)
	d := NewDecoderBytes([]byte("<item><item/><item/></item>"))
	*Internal.Export(&xmlopts.AllowInternalUse).StringCache(d) = new([256]string)
	tok, _ := d.ReadToken()
	tok.Name()
	if n := testing.AllocsPerRun(10, func() { tok.Name() }); n != 0 {
		t.Errorf("cached name: %v allocations, want 0", n)
	}
	if n := testing.AllocsPerRun(10, func() { makeString(nil, []byte("x")) }); n != 0 {
		t.Errorf("one-byte name: %v allocations, want 0", n)
	}
}

// The boundary batch goes back to its pool once a Decoder has a final EOF
// or error; later calls do not need it, and Reset starts afresh.
func TestBatchDroppedAtEnd(t *testing.T) {
	for _, tt := range []struct {
		in      string
		wantErr string
	}{
		{"<a><b>x</b></a>", "EOF"},
		{"<a><b>x</a>", "closed by"},
	} {
		d := NewDecoder(strings.NewReader(tt.in))
		var err error
		for err == nil {
			_, err = d.ReadToken()
		}
		if !strings.Contains(err.Error(), tt.wantErr) {
			t.Fatalf("%q: error %v, want %q", tt.in, err, tt.wantErr)
		}
		if d.bnd != nil {
			t.Errorf("%q: batch kept after %v", tt.in, err)
		}
		if _, err2 := d.ReadToken(); err2 != err {
			t.Errorf("%q: later ReadToken: %v, want %v", tt.in, err2, err)
		}
		if k := d.PeekKind(); k != Invalid {
			t.Errorf("%q: later PeekKind: %v", tt.in, k)
		}
		d.Reset(strings.NewReader("<c/>"))
		var n int
		for {
			if _, err := d.ReadToken(); err != nil {
				break
			}
			n++
		}
		if n != 2 {
			t.Errorf("%q: %d tokens after Reset, want 2", tt.in, n)
		}
	}
}

// A Decoder makes the strings of repeated names through its own cache once
// it has made a few, so that reading names does not allocate for each one;
// a small document makes its few names without a cache.
func TestDecoderOwnNames(t *testing.T) {
	testenv.SkipIfOptimizationOff(t)
	doc := []byte("<root>" + strings.Repeat(`<item id="1"><name>n</name></item>`, 100) + "</root>")
	names := func(d *Decoder) (n int) {
		for {
			tok, err := d.ReadToken()
			if err == io.EOF {
				return n
			}
			if err != nil {
				t.Fatal(err)
			}
			switch tok.Kind() {
			case StartElement, EndElement:
				_ = tok.Name()
				n++
				for it := tok.Attrs(); it.Next(); {
					_ = it.Name()
					n++
				}
			}
		}
	}
	d := NewDecoderBytes(doc)
	if n := names(d); n < 500 {
		t.Fatalf("read %d names", n)
	}
	if allocs := testing.AllocsPerRun(10, func() {
		d.ResetBytes(doc)
		names(d)
	}); allocs > 5 {
		t.Errorf("reading %d bytes of repeated names: %v allocs, want at most 5", len(doc), allocs)
	}

	small := []byte(`<a><bb x="1"/></a>`)
	if d := NewDecoderBytes(small); names(d) > 0 && d.ownNames != nil {
		t.Errorf("small document: Decoder allocated its own string cache")
	}
	d.Release()
	if d.ownNames == nil {
		t.Fatal("Release dropped the string cache")
	}
	for _, s := range d.ownNames {
		if s != "" {
			t.Fatalf("Release kept name %q", s)
		}
	}
}
