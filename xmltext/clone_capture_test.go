// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

// nameSnap is what a token reports about its names.
type nameSnap struct {
	name, raw   Name
	attrs, rawA []Name
	vals        []string
}

func snapNames(t Token) nameSnap {
	s := nameSnap{name: t.Name(), raw: t.RawName()}
	for it := t.Attrs(); it.Next(); {
		s.attrs = append(s.attrs, it.Name())
		s.rawA = append(s.rawA, it.RawName())
		s.vals = append(s.vals, string(it.Value()))
	}
	return s
}

func sameSnap(a, b nameSnap) bool {
	if a.name != b.name || a.raw != b.raw || len(a.attrs) != len(b.attrs) {
		return false
	}
	for i := range a.attrs {
		if a.attrs[i] != b.attrs[i] || a.rawA[i] != b.rawA[i] || a.vals[i] != b.vals[i] {
			return false
		}
	}
	return true
}

func TestCloneRawNames(t *testing.T) {
	cases := []struct {
		name string
		in   string
		opts []Options
		want string // RawName of the first StartElement, as a check that the case is meaningful
	}{
		{"prefixed", `<p:a xmlns:p="urn:u" p:x="1" y="2"></p:a>`, nil, "{p a}"},
		{"unprefixed", `<a x="1"></a>`, nil, "{ a}"},
		{"default ns", `<a xmlns="urn:d" x="1"><b/></a>`, nil, "{ a}"},
		{"xmlns decls", `<p:a xmlns:p="urn:u" xmlns="urn:d" xmlns:q="urn:q" q:z="3"/>`, nil, "{p a}"},
		{"xml prefix", `<a xml:lang="en"/>`, nil, "{ a}"},
		{"procinst", `<?xml version="1.0"?><?target data?><p:a xmlns:p="urn:u"/>`, nil, "{p a}"},
		{"permissive unbound", `<q:a q:x="1" xmlns:r="urn:r"><r:b/></q:a>`, []Options{Permissive(true)}, "{q a}"},
		{"permissive xmlns", `<xmlns:a xmlns:xmlns="urn:x" xmlns:p="urn:p"/>`, []Options{Permissive(true)}, "{xmlns a}"},
	}
	for _, c := range cases {
		for _, src := range []string{"bytes", "reader"} {
			t.Run(c.name+"/"+src, func(t *testing.T) {
				var d *Decoder
				if src == "bytes" {
					d = NewDecoderBytes([]byte(c.in), c.opts...)
				} else {
					d = NewDecoder(&chunkReader{s: c.in, n: 3}, c.opts...)
				}
				type pair struct {
					kind Kind
					orig nameSnap
					c    Token
					text string
				}
				var got []pair
				for {
					tk, err := d.ReadToken()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					got = append(got, pair{tk.Kind(), snapNames(tk), tk.Clone(), string(tk.Text())})
				}
				if len(got) == 0 {
					t.Fatal("no tokens")
				}
				sawWant := false
				for i, p := range got {
					if !sameSnap(p.orig, snapNames(p.c)) {
						t.Errorf("token %d (%v): clone %+v, original %+v", i, p.kind, snapNames(p.c), p.orig)
					}
					if !sameSnap(p.orig, snapNames(p.c.Clone())) {
						t.Errorf("token %d: clone of clone differs", i)
					}
					if string(p.c.Text()) != p.text {
						t.Errorf("token %d: clone text %q, original %q", i, p.c.Text(), p.text)
					}
					if p.kind == StartElement && !sawWant {
						sawWant = true
						if g := fmt.Sprint(p.c.RawName()); g != c.want {
							t.Errorf("first start RawName = %s, want %s", g, c.want)
						}
					}
				}
			})
		}
	}
}

// chunkReader returns at most n bytes per Read.
type chunkReader struct {
	s string
	n int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if len(r.s) == 0 {
		return 0, io.EOF
	}
	k := min(len(p), r.n, len(r.s))
	copy(p, r.s[:k])
	r.s = r.s[k:]
	return k, nil
}

func TestReadElementInsideCapture(t *testing.T) {
	big := strings.Repeat("<i k=\"v\">0123456789</i>", 3000) // ~66 KiB, well past the initial buffer
	elem := `<a x="1&amp;2"><b>t</b>` + big + `<c/></a>`
	elem2 := `<d>second</d>`
	inner := `pre` + elem + `mid<!--c-->` + elem2 + `post`
	in := `<root>` + inner + `</root>`

	mk := map[string]func() *Decoder{
		"bytes":  func() *Decoder { return NewDecoderBytes([]byte(in)) },
		"chunk7": func() *Decoder { return NewDecoder(&chunkReader{s: in, n: 7}) },
		"chunk5k": func() *Decoder {
			return NewDecoder(&chunkReader{s: in, n: 5000})
		},
	}
	for name, newD := range mk {
		t.Run(name, func(t *testing.T) {
			d := newD()
			if tk, err := d.ReadToken(); err != nil || tk.Kind() != StartElement {
				t.Fatalf("root: %v %v", tk.Kind(), err)
			}
			d.StartCapture()
			if tk, err := d.ReadToken(); err != nil || tk.Kind() != Text || string(tk.Text()) != "pre" {
				t.Fatalf("pre: %v %v", tk.Kind(), err)
			}
			raw, err := d.ReadElement()
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != elem {
				t.Fatalf("first element: got %d bytes, want %d", len(raw), len(elem))
			}
			if got, want := d.CaptureLen(), len(`pre`+elem); got != want {
				t.Fatalf("CaptureLen after element = %d, want %d", got, want)
			}
			if tk, err := d.ReadToken(); err != nil || string(tk.Text()) != "mid" {
				t.Fatalf("mid: %v %v", tk.Kind(), err)
			}
			if tk, err := d.ReadToken(); err != nil || tk.Kind() != Comment {
				t.Fatalf("comment: %v %v", tk.Kind(), err)
			}
			raw, err = d.ReadElement()
			if err != nil || string(raw) != elem2 {
				t.Fatalf("second element: %q %v", raw, err)
			}
			if tk, err := d.ReadToken(); err != nil || string(tk.Text()) != "post" {
				t.Fatalf("post: %v %v", tk.Kind(), err)
			}
			if got := string(d.Captured(0, d.CaptureLen())); got != inner {
				t.Fatalf("capture differs: got %d bytes, want %d", len(got), len(inner))
			}
			if tk, err := d.ReadToken(); err != nil || tk.Kind() != EndElement {
				t.Fatalf("end: %v %v", tk.Kind(), err)
			}
			d.StopCapture()
		})
	}
}

func TestReadElementWithoutCapture(t *testing.T) {
	d := NewDecoderBytes([]byte(`<a><b>x</b></a>`))
	d.ReadToken()
	raw, err := d.ReadElement()
	if err != nil || string(raw) != `<b>x</b>` {
		t.Fatalf("%q %v", raw, err)
	}
	if d.CaptureLen() != 0 {
		t.Fatal("capture left running")
	}
}

func TestReadElementInsideCaptureError(t *testing.T) {
	d := NewDecoderBytes([]byte(`<r>x<a><b></a></r>`))
	d.ReadToken()
	d.StartCapture()
	d.ReadToken()
	if _, err := d.ReadElement(); err == nil {
		t.Fatal("want mismatched tag error")
	}
}
