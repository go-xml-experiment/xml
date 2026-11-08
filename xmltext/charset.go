// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
)

// openCharset calls the converter while the declaration token is still current.
// The returned reader borrows buf[unreadAt:]; the caller must switch to a fresh
// buffer on the next decoder operation, not overwrite this unread input.
func (d *Decoder) openCharset(label string, unreadAt int) (io.Reader, error) {
	var input io.Reader = bytes.NewReader(d.buf[unreadAt:])
	if d.rerr != nil {
		input = io.MultiReader(input, charsetErrorReader{d.rerr})
	} else if !d.eof && d.r != nil {
		input = io.MultiReader(input, d.r)
	}
	// Legacy passes its buffered byte reader, including any unread read-ahead.
	if _, ok := input.(io.ByteReader); !ok {
		input = bufio.NewReader(input)
	}
	r, err := d.opts.CharsetReader(label, input)
	if err != nil {
		return nil, fmt.Errorf("xml: opening charset %q: %w", label, err)
	}
	if r == nil {
		panic("CharsetReader returned a nil Reader for charset " + label)
	}
	if rb, ok := r.(io.ByteReader); ok {
		return charsetByteReader{rb}, nil
	}
	return r, nil
}

// Legacy prefers ReadByte when the converted reader supplies it.
type charsetByteReader struct{ io.ByteReader }

func (r charsetByteReader) Read(p []byte) (int, error) {
	for i := range p {
		b, err := r.ReadByte()
		if err != nil {
			return i, err
		}
		p[i] = b
	}
	return len(p), nil
}

// A read error accompanying buffered bytes belongs after those bytes, not to
// the old decoder window that will be discarded when conversion starts.
type charsetErrorReader struct{ err error }

func (r charsetErrorReader) Read([]byte) (int, error) { return 0, r.err }

func (d *Decoder) applyCharset() {
	if d.charsetPending == nil {
		return
	}
	d.lineNo, d.lineStart = d.lineAt(d.pos)
	d.memoPos = 0
	if d.capturing {
		d.capBuf = append(d.capBuf, d.buf[d.capStart:d.pos]...)
		d.capStart = 0
	}
	d.base += int64(d.pos)
	d.r, d.charsetPending = d.charsetPending, nil
	// The converter may still borrow the old window's unread bytes.
	d.buf, d.ownBuf = make([]byte, 0, initialRead), true
	d.pos, d.linePos, d.scanPos = 0, 0, 0
	d.bi, d.bn = 0, 0
	d.eof, d.noMore = false, false
	d.rerr, d.engErr = nil, nil
	d.eng.reset(d.opts.MaxTokenSize, d.perm, d.opts.SplitText)
}
