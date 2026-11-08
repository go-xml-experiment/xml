// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// The caller's reader must be left exactly where encoding/xml leaves it after
// every token (see input.go), for each kind of reader that has a faster path.

type remaining func() int

type readerKind struct {
	name string
	// make returns the reader to hand to a Decoder and a function reporting
	// how many input bytes have not been consumed from the source.
	make func(in string) (io.Reader, remaining)
}

type plainByteReader struct{ r *strings.Reader } // a ByteReader of no special type

func (p plainByteReader) Read(b []byte) (int, error) { return p.r.Read(b) }
func (p plainByteReader) ReadByte() (byte, error)    { return p.r.ReadByte() }

var readerKinds = []readerKind{
	{"bytes.Reader", func(in string) (io.Reader, remaining) {
		r := bytes.NewReader([]byte(in))
		return r, r.Len
	}},
	{"strings.Reader", func(in string) (io.Reader, remaining) {
		r := strings.NewReader(in)
		return r, r.Len
	}},
	{"bufio.Reader", func(in string) (io.Reader, remaining) {
		u := strings.NewReader(in)
		r := bufio.NewReader(u)
		return r, func() int { return u.Len() + r.Buffered() }
	}},
	{"bufio.Reader(16)", func(in string) (io.Reader, remaining) {
		u := strings.NewReader(in)
		r := bufio.NewReaderSize(u, 16)
		return r, func() int { return u.Len() + r.Buffered() }
	}},
	{"bytes.Buffer", func(in string) (io.Reader, remaining) {
		r := bytes.NewBufferString(in)
		return r, r.Len
	}},
	{"ByteReader", func(in string) (io.Reader, remaining) {
		u := strings.NewReader(in)
		return plainByteReader{u}, u.Len
	}},
}

func positions(in string, rk readerKind) (out []string, err error) {
	r, rem := rk.make(in)
	d := NewDecoder(r)
	next := func() (string, error) { tk, e := d.Token(); return renderNew(tk), e }

	for len(out) < 2000 {
		s, e := next()
		if e != nil {
			out = append(out, fmt.Sprintf("err left=%d", rem()))
			return out, e
		}
		out = append(out, fmt.Sprintf("%s left=%d", s, rem()))
	}
	return out, nil
}

func TestReaderPositions(t *testing.T) {
	inputs := stdTestInputs(t)
	inputs = append(inputs, "<a>"+strings.Repeat("x", 10000)+"</a>trailing", "<a>"+strings.Repeat("<!-- c -->", 600)+"</a>", "<a "+strings.Repeat(`k="v" `, 2000)+"/>rest", "<a><![CDATA["+strings.Repeat("y", 9000)+"]]></a>", "<a>"+strings.Repeat("é", 5000)+"</a>\n")
	for _, rk := range readerKinds {
		for _, in := range inputs {
			got, err := positions(in, rk)
			want := fixedValue("corpus-positions", in, 0)
			// The original corpus check excludes source positions on a failing call.
			if err != nil {
				got = got[:len(got)-1]
			}
			if traceDigest(got) != want[0] || (err == nil) != (want[1] == "<nil>") {
				t.Errorf("%s %q: got %q (%v), want %s (%s)", rk.name, in, got, err, want[0], want[1])
			}
		}
	}
}
