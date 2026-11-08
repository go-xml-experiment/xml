// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"strconv"
	"strings"
	"testing"
	"time"
)

// shadowedURI writes k prefixes bound to "u" on <a>, rebinds all of them on
// <b> to other URIs, then k times opens <c> with an attribute in "u". With
// visible, <a> declares one more prefix z for "u" before the k (so it is the
// oldest, unshadowed binding) and the attribute is found there; without, no
// prefix for "u" is visible and each <c> generates one. A lookup walking the
// shadowed bindings costs k each: 66 ms for k=2500 and 3.4 s for 20000.
func shadowedURI(w io.Writer, k int, visible bool) error {
	e := NewEncoder(w)
	ns := XMLNSNamespace
	e.BeginElement(Name{Local: "a"})
	if visible {
		e.Attr(Name{Space: ns, Local: "z"}, "u")
	}
	for i := range k {
		e.Attr(Name{Space: ns, Local: "p" + strconv.Itoa(i)}, "u")
	}
	e.BeginElement(Name{Local: "b"})
	for i := range k {
		e.Attr(Name{Space: ns, Local: "p" + strconv.Itoa(i)}, "v"+strconv.Itoa(i))
	}
	for range k {
		e.BeginElement(Name{Local: "c"})
		e.Attr(Name{Space: "u", Local: "x"}, "1")
		e.EndElement()
	}
	e.EndElement()
	e.EndElement()
	return e.Close()
}

// shadowedURIDoc is the visible variant of shadowedURI as XML text, to be
// re-encoded token by token.
func shadowedURIDoc(k int) []byte {
	var b strings.Builder
	b.WriteString(`<a xmlns:z="u"`)
	for i := range k {
		fmt.Fprintf(&b, ` xmlns:p%d="u"`, i)
	}
	b.WriteString("><b")
	for i := range k {
		fmt.Fprintf(&b, ` xmlns:p%d="v%d"`, i, i)
	}
	b.WriteString(">")
	for range k {
		b.WriteString(`<c z:x="1"/>`)
	}
	b.WriteString("</b></a>")
	return []byte(b.String())
}

// Looking up a URI whose prefixes are all shadowed used to walk every one of
// them, k per attribute (4x time per doubling of k).
func TestEncoderShadowedURILookupLinear(t *testing.T) {
	const k = 20000
	for _, visible := range []bool{false, true} {
		t.Run(fmt.Sprintf("encoder/visible=%v", visible), func(t *testing.T) {
			start := time.Now()
			if err := shadowedURI(io.Discard, k, visible); err != nil {
				t.Fatal(err)
			}
			if el := time.Since(start); el > time.Second {
				t.Fatalf("k=%d took %v", k, el)
			}
		})
	}
	t.Run("reencode", func(t *testing.T) {
		data := shadowedURIDoc(k)
		start := time.Now()
		reencode(t, data)
		if el := time.Since(start); el > time.Second {
			t.Fatalf("%d bytes took %v", len(data), el)
		}
	})
}

func BenchmarkEncoderShadowedURI(b *testing.B) {
	for _, k := range []int{2500, 5000, 10000, 20000} {
		for _, visible := range []bool{false, true} {
			b.Run(fmt.Sprintf("k=%d/visible=%v", k, visible), func(b *testing.B) {
				for range b.N {
					shadowedURI(io.Discard, k, visible)
				}
			})
		}
		doc := shadowedURIDoc(k)
		b.Run(fmt.Sprintf("k=%d/reencode", k), func(b *testing.B) {
			b.SetBytes(int64(len(doc)))
			for range b.N {
				reencode(b, doc)
			}
		})
	}
}

// shadowSeq drives e with a random sequence over a few prefixes and URIs, so
// that bindings shadow each other, are restored when elements end, and
// collide with the generated ns<N> prefixes and the default namespace. It
// avoids the calls the Encoder refuses outright (duplicates, attributes after
// content) and returns the output and the first error, which can still come
// from a prefix redeclared after it was used.
func shadowSeq(e *Encoder, seed int64) (string, error) {
	rng := rand.New(rand.NewSource(seed))
	uris := []string{"u0", "u1", "u2", "u3"}
	prefixes := []string{"p", "q", "r", "s", "ns1", "ns2", "ns3", "ns4"}
	var b bytes.Buffer
	e.Reset(&b)
	depth := 0
	inStart := false
	used := map[Name]bool{}
	qualified := false
	begin := func(name Name) error {
		clear(used)
		depth++
		inStart = true
		qualified = name.Space != ""
		return e.BeginElement(name)
	}
	if err := begin(Name{Space: "u0", Local: "root"}); err != nil {
		return b.String(), err
	}
	for n := 60 + rng.Intn(60); n > 0; n-- {
		var err error
		switch op := rng.Intn(10); {
		case op < 3 && depth < 14:
			name := Name{Local: "e"}
			if rng.Intn(3) != 0 {
				name.Space = uris[rng.Intn(len(uris))]
			}
			err = begin(name)
		case op < 6 && inStart:
			name := Name{Space: XMLNSNamespace, Local: prefixes[rng.Intn(len(prefixes))]}
			if !used[name] {
				used[name] = true
				err = e.Attr(name, uris[rng.Intn(len(uris))])
			}
		case op == 6 && inStart && qualified:
			name := Name{Space: XMLNSNamespace, Local: "xmlns"}
			if !used[name] {
				used[name] = true
				err = e.Attr(name, uris[rng.Intn(len(uris))])
			}
		case op < 9 && inStart:
			name := Name{Space: uris[rng.Intn(len(uris))], Local: "a" + strconv.Itoa(rng.Intn(3))}
			if !used[name] {
				used[name] = true
				err = e.Attr(name, "1")
			}
		case depth > 1:
			err = e.EndElement()
			depth--
			inStart = false
		}
		if err != nil {
			return b.String(), err
		}
	}
	for ; depth > 0; depth-- {
		if err := e.EndElement(); err != nil {
			return b.String(), err
		}
	}
	err := e.Close()
	return b.String(), err
}

// Indexed lookups (with the index starting from the first binding, a few
// bindings in, or never) give the same bytes and errors as plain scans over
// random declare, shadow and pop sequences.
func TestEncoderShadowIndexMatchesScan(t *testing.T) {
	saved := encNSMin
	defer func() { encNSMin = saved }()
	var e Encoder
	accepted := 0
	for seed := int64(0); seed < 4000; seed++ {
		encNSMin = 1 << 30
		want, wantErr := shadowSeq(&e, seed)
		if wantErr == nil {
			accepted++
		}
		for _, min := range []int{0, 1, 3, 6} {
			encNSMin = min
			got, gotErr := shadowSeq(&e, seed)
			if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
				t.Fatalf("seed %d, encNSMin %d: indexed differs from scanned\n  %q (%v)\n  %q (%v)", seed, min, got, gotErr, want, wantErr)
			}
		}
	}
	if accepted < 2000 {
		t.Fatalf("only %d of 4000 sequences were accepted", accepted)
	}
}

// commonNS writes the shape most documents have: a handful of prefixes
// declared on the root, many namespaced elements and attributes below, and
// the odd generated prefix.
func commonNS(w io.Writer, rows int) error {
	e := NewEncoder(w)
	ns := XMLNSNamespace
	e.BeginElement(Name{Space: "urn:a", Local: "root"})
	e.Attr(Name{Space: ns, Local: "xmlns"}, "urn:a")
	e.Attr(Name{Space: ns, Local: "b"}, "urn:b")
	e.Attr(Name{Space: ns, Local: "c"}, "urn:c")
	for range rows {
		e.BeginElement(Name{Space: "urn:b", Local: "row"})
		e.Attr(Name{Space: "urn:c", Local: "id"}, "1")
		e.Attr(Name{Local: "plain"}, "x")
		e.Attr(Name{Space: "urn:d", Local: "gen"}, "y")
		e.BeginElement(Name{Space: "urn:a", Local: "cell"})
		e.Text("v")
		e.EndElement()
		e.EndElement()
	}
	e.EndElement()
	return e.Close()
}

func BenchmarkEncoderCommon(b *testing.B) {
	b.Run("namespaced", func(b *testing.B) {
		for range b.N {
			commonNS(io.Discard, 1000)
		}
	})
	b.Run("plain", func(b *testing.B) {
		for range b.N {
			e := NewEncoder(io.Discard)
			e.BeginElement(Name{Local: "root"})
			for range 1000 {
				e.BeginElement(Name{Local: "row"})
				e.Attr(Name{Local: "id"}, "1")
				e.Text("v")
				e.EndElement()
			}
			e.EndElement()
			e.Close()
		}
	})
}
