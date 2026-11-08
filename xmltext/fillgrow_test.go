// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"io"
	"math"
	"strings"
	"testing"
)

// lenReader reports a Len that may differ from what is really left.
type lenReader struct {
	r   *bytes.Reader
	fix func(real int) int
}

func (l *lenReader) Read(p []byte) (int, error) { return l.r.Read(p) }
func (l *lenReader) Len() int                   { return l.fix(l.r.Len()) }

// TestFillLenHint checks that a wrong Len hint cannot change the tokens, and
// that an exact one keeps the read buffer near the input size instead of the
// next doubling.
func TestFillLenHint(t *testing.T) {
	doc := "<a>" + strings.Repeat("x&amp;y&#65;\r\n", 90000) + "<b k='" + strings.Repeat("&lt;", 3000) + "'/></a>"
	read := func(r io.Reader) (toks []string, bufCap int) {
		d := NewDecoder(r)
		for {
			tok, err := d.ReadToken()
			if err != nil {
				break
			}
			toks = append(toks, tok.Kind().String()+":"+string(tok.Text()))
		}
		return toks, cap(d.buf)
	}
	want, _ := read(strings.NewReader(doc))
	for name, fix := range map[string]func(int) int{
		"exact": func(n int) int { return n },
		"zero":  func(int) int { return 0 },
		"neg":   func(int) int { return -5 },
		"huge":  func(int) int { return math.MaxInt },
	} {
		got, _ := read(&lenReader{bytes.NewReader([]byte(doc)), fix})
		if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
			t.Errorf("%s: tokens differ from a reader without Len", name)
		}
	}
	_, exact := read(bytes.NewReader([]byte(doc)))
	_, plain := read(&lenReader{bytes.NewReader([]byte(doc)), func(int) int { return math.MaxInt }})
	if exact > len(doc)+minRead || exact >= plain {
		t.Errorf("buffer cap with exact Len = %d, want <= %d and below %d (no hint)", exact, len(doc)+minRead, plain)
	}
}

// plainReader hides every optional method (Len, WriterTo) of the reader it wraps.
type plainReader struct{ r io.Reader }

func (p plainReader) Read(b []byte) (int, error) { return p.r.Read(b) }

// TestFirstBuffer checks the Reader buffer policy: a first buffer sized by a Len
// hint (at most a window), 4 KiB without one, a jump to a window when the
// input keeps filling the buffer, and no change in the tokens.
func TestFirstBuffer(t *testing.T) {
	small := `<a x="1"><b>text</b></a>`
	read := func(r io.Reader) (n int, bufCap int) {
		d := NewDecoder(r)
		for {
			if _, err := d.ReadToken(); err != nil {
				break
			}
			n++
		}
		return n, cap(d.buf)
	}
	medium := "<a>" + strings.Repeat("<b>x</b>", 400) + "</a>" // about 3.2 KB
	big := "<a>" + strings.Repeat("<b>x</b>", 200000) + "</a>"
	for _, c := range []struct {
		name   string
		r      io.Reader
		tokens int
		cap    int
	}{
		{"small, Len", strings.NewReader(small), 5, minInitialRead},
		{"small, no Len", plainReader{strings.NewReader(small)}, 5, initialRead},
		{"medium, Len", strings.NewReader(medium), 1202, len(medium) + 1},
		{"medium, no Len", plainReader{strings.NewReader(medium)}, 1202, initialRead},
		{"big, Len", strings.NewReader(big), 600002, minRead},
		{"big, no Len", plainReader{strings.NewReader(big)}, 600002, minRead},
	} {
		n, got := read(c.r)
		if n != c.tokens {
			t.Errorf("%s: %d tokens, want %d", c.name, n, c.tokens)
		}
		if got != c.cap {
			t.Errorf("%s: buffer cap %d, want %d", c.name, got, c.cap)
		}
	}
}
