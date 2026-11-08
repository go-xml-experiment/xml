// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"fmt"
	"io"

	"strings"
	"testing"
	"time"
)

// bufferKind returns a bytes.Buffer whose first n bytes were already read.
func bufferKind(skip int) readerKind {
	return readerKind{name: "bytes.Buffer", make: func(in string) (io.Reader, remaining) {
		b := bytes.NewBufferString(strings.Repeat("?", skip) + in)
		b.Next(skip)
		return b, b.Len
	}}
}

func TestBufferSourceWindow(t *testing.T) {
	input := "<r/>" + strings.Repeat("x", 16<<20)
	b := bytes.NewBufferString(input)
	s := newSource(b)
	p := make([]byte, 8192)
	n, err := s.reader().Read(p)
	if err != nil || n != len(p) || string(p[:4]) != "<r/>" {
		t.Fatalf("Read = %d, %v, prefix %q", n, err, p[:4])
	}
	if b.Len() != len(input) {
		t.Fatalf("read-ahead moved caller: remaining %d, want %d", b.Len(), len(input))
	}
	s.commit(4, false)
	if b.Len() != len(input)-4 {
		t.Fatalf("commit left %d bytes, want %d", b.Len(), len(input)-4)
	}
}

func TestBufferSourceAgainstLegacy(t *testing.T) {
	inputs := []string{
		"", "<r/>", "<r>text</r>tail", "text", "<r>", "<r a=\"unfinished", "<r>a&amp;b</r>",
		"<r>" + strings.Repeat("x", 4092) + "</r>",
		"<r>" + strings.Repeat("x", 32766) + "</r>tail",
		"<r>" + strings.Repeat("x", 1<<20) + "</r>tail",
		"<r>" + strings.Repeat("한글\r\n&amp;", 1<<16) + "</r>tail",
		"<" + strings.Repeat("n", 5000) + "/>",
		"<r a=\"" + strings.Repeat("x", 9000) + "\"/>",
		"<r><!--" + strings.Repeat("x", 9000) + "--></r>",
		"<r><![CDATA[" + strings.Repeat("x", 9000) + "]]></r>",
	}
	for _, skip := range []int{0, 7} {
		rk := bufferKind(skip)
		for i, input := range inputs {
			t.Run(fmt.Sprintf("skip=%d/%d", skip, i), func(t *testing.T) {
				want := fixedValue("positions", input, 0)
				got, gerr := positions(input, rk)
				if traceDigest(got) != want[0] || fmt.Sprint(gerr) != want[1] {
					t.Fatalf("legacy: %q (%v)\n   new: %q (%v)", want[0], want[1], got, gerr)
				}
			})
		}
	}
}

// With an entity map the settings are consulted at every Read, and the
// positions must still match.
func TestBufferSourceEntityPosition(t *testing.T) {
	const item = `<i a="&amp;&lt;">x &amp; y &nbsp; z</i>`
	input := "<r>" + strings.Repeat(item, 200) + "tail</r>rest"
	r := bytes.NewBufferString(input)
	d := NewDecoder(r)
	d.Strict, d.Entity = false, HTMLEntity
	if tk, e := d.Token(); e != nil || renderNew(tk) != `S "" "r" ""` {
		t.Fatalf("root: %v %v", tk, e)
	}
	for i := 0; i < 200; i++ {
		for _, want := range []string{`S "" "i" "{\"\" \"a\" \"&<\"}"`, `T "x & y \u00a0 z"`, `E "" "i"`} {
			tk, e := d.Token()
			if e != nil || renderNew(tk) != want {
				t.Fatalf("item %d: %s (%v), want %s", i, renderNew(tk), e, want)
			}
		}
		if want := len(input) - 3 - (i+1)*len(item); r.Len() != want {
			t.Fatalf("item %d: remaining %d, want %d", i, r.Len(), want)
		}
	}
}

// A buffer that grows between Token calls keeps feeding the decoder, as a
// ByteReader would.
func TestBufferSourceGrowingBuffer(t *testing.T) {
	var b bytes.Buffer
	d := NewDecoder(&b)
	b.WriteString("<r>")
	if tok, err := d.Token(); err != nil || tok.(StartElement).Name.Local != "r" {
		t.Fatalf("first token = %#v, %v", tok, err)
	}
	b.WriteString("<a/></r>")
	var got []string
	for {
		tok, err := d.Token()
		if err != nil {
			if err != io.EOF {
				t.Fatal(err)
			}
			break
		}
		got = append(got, renderNew(tok))
	}
	if len(got) != 3 || b.Len() != 0 {
		t.Fatalf("tokens %q, %d bytes left", got, b.Len())
	}
}

func TestBufferSourceCharsetRemainder(t *testing.T) {
	const declaration = `<?xml version="1.0" encoding="window-test"?>`
	body := "<r>" + strings.Repeat("x", 9000) + "</r>"
	b := bytes.NewBufferString("skipped:" + declaration + body)
	b.Next(len("skipped:"))
	d := NewDecoder(b)
	calls := 0
	d.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
		calls++
		remainder, err := io.ReadAll(input)
		if err != nil || string(remainder) != body {
			t.Fatalf("charset remainder length = %d (%v), want %d", len(remainder), err, len(body))
		}
		return bytes.NewReader(remainder), nil
	}
	var text []byte
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if data, ok := token.(CharData); ok {
			text = append(text, data...)
		}
	}
	if calls != 1 || string(text) != strings.Repeat("x", 9000) {
		t.Fatalf("charset calls = %d, decoded text length = %d", calls, len(text))
	}
}

// Decoding with an Entity map from a bytes.Buffer must take time proportional
// to the input, not input x map size (5.5 s before the buffer source, ~30 ms after).
func TestBufferSourceEntityLinear(t *testing.T) {
	data := entityBenchInput()
	d := NewDecoder(bytes.NewBuffer(data))
	d.Strict, d.Entity = false, HTMLEntity
	start := time.Now()
	tokens := 0
	for {
		if _, err := d.Token(); err != nil {
			break
		}
		tokens++
	}
	if tokens < 60000 {
		t.Fatalf("only %d tokens", tokens)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("%d bytes took %v", len(data), el)
	}
}
