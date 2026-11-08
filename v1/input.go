// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bufio"
	"bytes"
	"io"
	"strings"
)

// encoding/xml reads a ByteReader one byte at a time, so after a token the
// caller's reader is exactly one byte past the token if the token was text
// (the '<' that ended it was read and pushed back inside the Decoder) and
// exactly at the token's end otherwise. Feeding the tokenizer byte by byte keeps
// that, and is slow. For the reader types whose state can be inspected and
// moved without consuming input (*bytes.Reader, *strings.Reader, *bufio.Reader,
// *bytes.Buffer) the tokenizer reads a bounded ReadAt, peeked or copied window
// instead, and the caller's reader is advanced to the same position after every
// token.

// source feeds the tokenizer and reports how much the caller's reader has to
// advance after a token.
type source interface {
	// reader is the io.Reader the tokenizer reads, or nil if data is used.
	reader() io.Reader
	// bytes is the complete remaining input, or nil if reader is used.
	bytes() []byte
	// commit advances the caller's reader after a token that ends at stream
	// offset off. textEnd is set after a text token, which encoding/xml has
	// already read one byte past (the '<' that ended it), when there is one.
	commit(off int64, textEnd bool)
}

func newSource(r io.Reader) source {
	switch r := r.(type) {
	case *bytes.Reader:
		return newMemSource(r.Len(), r.Size(), r.ReadAt, r)
	case *strings.Reader:
		return newMemSource(r.Len(), r.Size(), r.ReadAt, r)
	case *bufio.Reader:
		return &bufioSource{br: r}
	case *bytes.Buffer:
		return &bufferSource{buf: r}
	}
	if br, ok := r.(io.ByteReader); ok {
		return &byteSource{br: br}
	}
	return &plainSource{r: r}
}

type plainSource struct{ r io.Reader }

func (s *plainSource) reader() io.Reader  { return chunked{s.r} }
func (s *plainSource) bytes() []byte      { return nil }
func (s *plainSource) commit(int64, bool) {}

type byteSource struct{ br io.ByteReader }

func (s *byteSource) reader() io.Reader  { return byteAtATime{s.br} }
func (s *byteSource) bytes() []byte      { return nil }
func (s *byteSource) commit(int64, bool) {}

// memSource reads bounded windows without moving the caller's reader.
// The tokenizer owns the only input buffer; commit advances the reader later.
type memSource struct {
	readAt    func([]byte, int64) (int, error)
	seeker    io.Seeker
	start     int64
	length    int64
	ahead     int64
	committed int64
}

func newMemSource(n int, size int64, readAt func([]byte, int64) (int, error), seeker io.Seeker) source {
	return &memSource{readAt: readAt, seeker: seeker, start: size - int64(n), length: int64(n)}
}

func (s *memSource) reader() io.Reader { return s }
func (s *memSource) bytes() []byte     { return nil }

// Len is the number of bytes Read has yet to return; xmltext uses it to size its buffer.
func (s *memSource) Len() int { return int(s.length - s.ahead) }

func (s *memSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.ahead >= s.length {
		return 0, io.EOF
	}
	p = p[:min(len(p), int(s.length-s.ahead))]
	n, err := s.readAt(p, s.start+s.ahead)
	s.ahead += int64(n)
	return n, err
}

func (s *memSource) commit(off int64, textEnd bool) {
	target := off
	if textEnd && off < s.length {
		target++
	}
	if target > s.length {
		target = s.length
	}
	if target > s.committed {
		s.seeker.Seek(target-s.committed, io.SeekCurrent)
		s.committed = target
	}
}

// bufioSource lets the tokenizer read the bytes a bufio.Reader holds (Peek)
// without consuming them; commit discards what the tokens used. A token larger
// than the buffer is fed through ReadByte instead, as encoding/xml does.
type bufioSource struct {
	br        *bufio.Reader
	ahead     int   // bytes handed to the tokenizer but still in br
	committed int64 // bytes removed from br so far
}

func (s *bufioSource) reader() io.Reader { return s }
func (s *bufioSource) bytes() []byte     { return nil }

func (s *bufioSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if s.br.Buffered() <= s.ahead {
		if _, err := s.br.Peek(s.ahead + 1); err != nil {
			if err == bufio.ErrBufferFull {
				return s.readSlow(p)
			}
			return 0, err
		}
	}
	n := min(len(p), s.br.Buffered()-s.ahead)
	buf, err := s.br.Peek(s.ahead + n)
	if err != nil && len(buf) <= s.ahead {
		return 0, err
	}
	n = copy(p, buf[s.ahead:])
	s.ahead += n
	return n, nil
}

// readSlow takes one byte after dropping the (incomplete, oversized) token
// prefix the tokenizer already holds.
func (s *bufioSource) readSlow(p []byte) (int, error) {
	s.br.Discard(s.ahead)
	s.committed += int64(s.ahead)
	s.ahead = 0
	c, err := s.br.ReadByte()
	if err != nil {
		return 0, err
	}
	s.committed++
	p[0] = c
	return 1, nil
}

func (s *bufioSource) commit(off int64, textEnd bool) {
	target := off
	if textEnd && s.committed+int64(s.ahead) > off {
		target++
	}
	if n := target - s.committed; n > 0 {
		if int(n) > s.ahead {
			n = int64(s.ahead)
		}
		s.br.Discard(int(n))
		s.ahead -= int(n)
		s.committed += n
	}
}

// bufferSource copies windows of a bytes.Buffer's unread bytes; commit drops
// what the tokens used (Next). Bytes written to the buffer later are seen by the
// next Read, as with ReadByte.
type bufferSource struct {
	buf   *bytes.Buffer
	ahead int // bytes handed to the tokenizer but still in buf
	done  int64
}

func (s *bufferSource) reader() io.Reader { return s }
func (s *bufferSource) bytes() []byte     { return nil }

// Len is the number of bytes Read has yet to return; xmltext uses it to size its buffer.
func (s *bufferSource) Len() int { return s.buf.Len() - s.ahead }

func (s *bufferSource) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	rest := s.buf.Bytes()[s.ahead:]
	if len(rest) == 0 {
		return 0, io.EOF
	}
	n := copy(p, rest)
	s.ahead += n
	return n, nil
}

func (s *bufferSource) commit(off int64, textEnd bool) {
	target := off
	if textEnd && s.done+int64(s.ahead) > off {
		target++
	}
	if n := target - s.done; n > 0 {
		if int(n) > s.ahead {
			n = int64(s.ahead)
		}
		s.buf.Next(int(n))
		s.ahead -= int(n)
		s.done += n
	}
}

// dataSource is a document held in memory that no caller's reader backs.
type dataSource struct{ data []byte }

func (s dataSource) reader() io.Reader  { return nil }
func (s dataSource) bytes() []byte      { return s.data }
func (s dataSource) commit(int64, bool) {}
