// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"io"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
)

// dump reads every token of d and renders it, with names, attributes and text.
func dump(d *Decoder) string {
	var sb strings.Builder
	for {
		tk, err := d.ReadToken()
		if err != nil {
			fmt.Fprintf(&sb, "<%v>", err)
			return sb.String()
		}
		fmt.Fprintf(&sb, "%s", tk.Kind())
		switch tk.Kind() {
		case StartElement, EndElement:
			fmt.Fprintf(&sb, "%v", tk.Name())
			for it := tk.Attrs(); it.Next(); {
				fmt.Fprintf(&sb, " %v=%q", it.Name(), it.Value())
			}
		default:
			fmt.Fprintf(&sb, "%q", tk.Text())
		}
		sb.WriteByte('\n')
	}
}

var releaseDocs = []string{
	`<a x="1"><b>text</b><c/></a>`,
	`<p:a xmlns:p="urn:u" xmlns="urn:d" p:x="1"><b p:y="&amp;"/><![CDATA[c]]><!--k--></p:a>`,
	`<?xml version="1.0"?><r>` + strings.Repeat("<i a='1' b='2'>&lt;x&gt;</i>", 50) + `</r>`,
	`<a><b></a>`, // error
	``,           // error
	`<r xmlns:a="1" xmlns:b="2" xmlns:c="3" xmlns:d="4" xmlns:e="5" xmlns:f="6" xmlns:g="7" xmlns:h="8" xmlns:i="9" xmlns:j="10" xmlns:k="11" xmlns:l="12" xmlns:m="13" xmlns:n="14" xmlns:o="15" xmlns:p="16" xmlns:q="17"><a:x/><q:y/></r>`,
}

// TestReleaseReuse checks that a Decoder that was Released and Reset reads
// every document exactly as a new one does, whatever it read before.
func TestReleaseReuse(t *testing.T) {
	opts := [][]Options{nil, {Permissive(true)}, {SplitText(true)}}
	reused := new(Decoder)
	for round := 0; round < 3; round++ {
		for _, o := range opts {
			for _, doc := range releaseDocs {
				want := dump(NewDecoderBytes([]byte(doc), o...))
				reused.ResetBytes([]byte(doc), o...)
				if got := dump(reused); got != want {
					t.Fatalf("bytes %q: reused decoder read\n%s\nwant\n%s", doc, got, want)
				}
				reused.Release()
				wantR := dump(NewDecoder(strings.NewReader(doc), o...))
				reused.Reset(strings.NewReader(doc), o...)
				if got := dump(reused); got != wantR {
					t.Fatalf("reader %q: reused decoder read\n%s\nwant\n%s", doc, got, wantR)
				}
				reused.Release()
			}
		}
	}
}

// TestReleaseKeepsOwnBuffer checks that a Reader's buffer survives Release (and
// is used again by Reset), while the caller's input never does.
func TestReleaseKeepsOwnBuffer(t *testing.T) {
	d := NewDecoder(strings.NewReader(releaseDocs[2]))
	dump(d)
	c := cap(d.buf)
	if n := d.Release(); n < c {
		t.Errorf("Release kept %d bytes, want at least the %d-byte read buffer", n, c)
	}
	d.Reset(strings.NewReader(releaseDocs[2]))
	if cap(d.buf) != c {
		t.Errorf("read buffer cap after Reset = %d, want the kept %d", cap(d.buf), c)
	}
	in := []byte(releaseDocs[0])
	d.ResetBytes(in)
	dump(d)
	d.Release()
	if d.buf != nil {
		t.Error("Release kept the caller's input")
	}
}

// TestReleaseBigBuffers checks that nothing above 64 KiB is kept.
func TestReleaseBigBuffers(t *testing.T) {
	big := strings.Repeat("x", 200<<10)
	docs := []string{
		`<a>` + big + `</a>`,
		`<a k="` + big + `"/>`,
		`<a>` + strings.Repeat("&amp;", 100<<10) + `</a>`,
		strings.Repeat("<a>", 30000) + strings.Repeat("</a>", 30000),
	}
	for i, doc := range docs {
		for _, src := range []string{"bytes", "reader"} {
			d := new(Decoder)
			o := []Options{MaxDepth(100000)}
			if src == "bytes" {
				d.ResetBytes([]byte(doc), o...)
			} else {
				d.Reset(plainReader{strings.NewReader(doc)}, o...)
			}
			if i == 0 {
				d.StartCapture()
			}
			dump(d)
			if n := d.Release(); n > 8*maxRetained {
				t.Errorf("doc %d %s: Release kept %d bytes", i, src, n)
			}
			for name, c := range map[string]int{
				"buf": sizeOf(d.buf), "scratch": sizeOf(d.scratch), "attrBuf": sizeOf(d.attrBuf),
				"nameArena": sizeOf(d.nameArena), "stack": sizeOf(d.stack), "bindings": sizeOf(d.bindings),
				"capBuf": sizeOf(d.capBuf), "attrs": sizeOf(d.attrs),
			} {
				if c > maxRetained {
					t.Errorf("doc %d %s: %s kept %d bytes, want at most %d", i, src, name, c, maxRetained)
				}
			}
			// still usable afterwards
			d.ResetBytes([]byte(releaseDocs[0]))
			if got, want := dump(d), dump(NewDecoderBytes([]byte(releaseDocs[0]))); got != want {
				t.Errorf("doc %d %s: after a big document, read\n%s\nwant\n%s", i, src, got, want)
			}
		}
	}
}

// TestReleaseUnusable checks that a Released Decoder fails instead of reading.
func TestReleaseUnusable(t *testing.T) {
	d := NewDecoderBytes([]byte(releaseDocs[0]))
	d.ReadToken()
	d.Release()
	if _, err := d.ReadToken(); err == nil || err == io.EOF {
		t.Errorf("ReadToken after Release = %v, want an error", err)
	}
	if k := d.PeekKind(); k != Invalid {
		t.Errorf("PeekKind after Release = %v, want Invalid", k)
	}
}

// TestReleaseDropsInput checks that a Released Decoder keeps nothing the input
// or the options pointed to: the bytes, the Reader, what the charset function
// captures.
func TestReleaseDropsInput(t *testing.T) {
	d := new(Decoder)
	collected := make(chan string, 8)
	opts := func() []Options {
		p := new([32]int) // what the charset function captures (not a tiny allocation)
		fn := func(string, io.Reader) (io.Reader, error) { p[0]++; return nil, nil }
		runtime.AddCleanup(p, func(string) { collected <- "charset" }, "")
		return []Options{WithEntities(map[string]string{"foo": "bar"}), WithCharsetReader(fn)}
	}
	doc := "<r>" + strings.Repeat("<a b='1'>x</a>", 4) + "</r>"
	phases := map[string]func(){
		"bytes": func() {
			in := []byte(doc)
			runtime.AddCleanup(&in[0], func(string) { collected <- "input" }, "")
			d.ResetBytes(in, opts()...)
			dump(d)
			d.Release()
		},
		"reader": func() {
			rd := &struct{ r io.Reader }{strings.NewReader(doc)}
			runtime.AddCleanup(rd, func(string) { collected <- "input" }, "")
			d.Reset(readerOf(rd), opts()...)
			dump(d)
			d.Release()
		},
	}
	for _, phase := range []string{"bytes", "reader"} {
		phases[phase]()
		got := map[string]bool{}
		for i := 0; i < 20 && len(got) < 2; i++ {
			runtime.GC()
			time.Sleep(time.Millisecond)
			for len(collected) > 0 {
				got[<-collected] = true
			}
		}
		for _, what := range []string{"input", "charset"} {
			if !got[what] {
				t.Errorf("%s: the %s is still reachable from a Released Decoder", phase, what)
			}
		}
	}
	runtime.KeepAlive(d)
}

func readerOf(p *struct{ r io.Reader }) io.Reader {
	return readerFunc(func(b []byte) (int, error) { return p.r.Read(b) })
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(b []byte) (int, error) { return f(b) }

// TestEncoderRelease checks that a Released and Reset Encoder writes exactly
// what a new one does, keeps small buffers and drops big ones.
func TestEncoderRelease(t *testing.T) {
	write := func(e *Encoder, n int) {
		e.BeginElement(Name{Local: "r"})
		for i := 0; i < n; i++ {
			e.BeginElement(Name{Space: "urn:x", Local: "i"})
			e.Attr(Name{Local: "k"}, strings.Repeat("v", 100))
			e.Attr(Name{Space: "urn:y", Local: "a"}, "1")
			e.Text("t&")
			e.EndElement()
		}
		e.EndElement()
		e.Close()
	}
	for _, o := range [][]Options{nil, {xmlopts.LegacyOutput(true)}} {
		var want bytes.Buffer
		write(NewEncoder(&want, o...), 3)
		e := new(Encoder)
		for _, n := range []int{3, 1000, 3} {
			var got bytes.Buffer
			e.Reset(&got, o...)
			write(e, n)
			if n == 3 && got.String() != want.String() {
				t.Errorf("reused encoder wrote %q, want %q", got.String(), want.String())
			}
			kept := e.Release()
			if n == 1000 && kept > 8*maxRetained {
				t.Errorf("Release kept %d bytes after a big document", kept)
			}
			for name, c := range map[string]int{"buf": sizeOf(e.buf), "pbuf": sizeOf(e.pbuf), "arena": sizeOf(e.arena),
				"tattrs": sizeOf(e.tattrs), "stack": sizeOf(e.stack), "binds": sizeOf(e.binds)} {
				if c > maxRetained {
					t.Errorf("n=%d: %s kept %d bytes", n, name, c)
				}
			}
			if e.w != nil || e.attrSet != nil {
				t.Error("Release kept the writer or a map")
			}
		}
	}
}

// TestEncoderReleaseDropsNames checks that a Released Encoder keeps none of the
// strings handed to it: names, namespaces, attribute values and the Writer.
func TestEncoderReleaseDropsNames(t *testing.T) {
	collected := make(chan string, 8)
	e := new(Encoder)
	mk := func(what string, c byte) string {
		mem := []byte(strings.Repeat(string(c), 40))
		runtime.AddCleanup(&mem[0], func(string) { collected <- what }, "")
		return unsafe.String(&mem[0], len(mem))
	}
	for _, legacy := range []bool{false, true} {
		var o []Options
		if legacy {
			o = append(o, xmlopts.LegacyOutput(true))
		}
		func() {
			w := &struct{ bytes.Buffer }{}
			runtime.AddCleanup(w, func(string) { collected <- "writer" }, "")
			e.Reset(w, o...)
			space, local, aspace, alocal, ns := mk("space", 's'), mk("local", 'l'), mk("attr space", 'a'), mk("attr local", 'b'), mk("prefix", 'p')
			e.BeginElement(Name{Local: "root"})
			e.BeginElement(Name{Space: space, Local: local})
			e.Attr(Name{Space: aspace, Local: alocal}, "v")
			e.Attr(Name{Space: XMLNSNamespace, Local: ns[:5]}, space)
			e.Text("x")
			e.BeginElement(Name{Local: mk("eager local", 'e')})
			e.EndElement()
			e.EndElement()
			e.EndElement()
			e.Close()
			e.Release()
		}()
		want := []string{"writer", "eager local"}
		if !legacy {
			want = append(want, "space", "local", "attr space", "attr local", "prefix")
		}
		got := map[string]bool{}
		runtime.GC()
		for deadline := time.Now().Add(2 * time.Second); len(got) < len(want) && time.Now().Before(deadline); {
			time.Sleep(time.Millisecond)
			for len(collected) > 0 {
				got[<-collected] = true
			}
		}
		for _, w := range want {
			if !got[w] {
				t.Errorf("legacy=%v: the %s is still reachable from a Released Encoder", legacy, w)
			}
		}
		clear(got)
		for len(collected) > 0 {
			<-collected
		}
	}
	runtime.KeepAlive(e)
}
