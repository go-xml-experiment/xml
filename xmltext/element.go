// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import "errors"

// ReadElement reads the next element, which must be a StartElement, through
// its matching EndElement and returns its raw bytes. The result is valid
// until the next Decoder call. It may be called while a StartCapture capture
// is active: the capture goes on untouched and the result is then a copy.
func (d *Decoder) ReadElement() (Raw, error) {
	if k := d.PeekKind(); k != StartElement {
		return nil, d.notElement(k)
	}
	if d.capturing {
		from := d.CaptureLen()
		if err := d.skipCurrent(); err != nil {
			return nil, err
		}
		return Raw(d.Captured(from, d.CaptureLen())), nil
	}
	d.capturing, d.capStart = true, d.pos
	d.capBuf = d.capBuf[:0]
	defer func() { d.capturing = false }()
	if err := d.skipCurrent(); err != nil {
		return nil, err
	}
	if len(d.capBuf) == 0 {
		return Raw(d.buf[d.capStart:d.pos]), nil
	}
	d.capBuf = append(d.capBuf, d.buf[d.capStart:d.pos]...)
	return Raw(d.capBuf), nil
}

// SkipElement skips the next element, which must be a StartElement,
// validating its content.
func (d *Decoder) SkipElement() error {
	if k := d.PeekKind(); k != StartElement {
		return d.notElement(k)
	}
	return d.skipCurrent()
}

func (d *Decoder) notElement(k Kind) error {
	if d.err != nil {
		return d.err
	}
	if k == Invalid && d.rerr != nil {
		return d.rerr
	}
	return errors.New("xmltext: next token is " + k.String() + ", not StartElement")
}

func (d *Decoder) skipCurrent() error {
	depth := 0
	for {
		t, err := d.ReadToken()
		if err != nil {
			return err
		}
		switch t.Kind() {
		case StartElement:
			depth++
		case EndElement:
			depth--
			if depth == 0 {
				return nil
			}
		}
	}
}

// StartCapture begins recording the raw input from the current position, as
// for ReadElement but across any number of tokens. Offsets passed to Captured
// count from this position. It exists for the v1 layer's ,innerxml support.
// Starting a capture while one is active is a no-op.
func (d *Decoder) StartCapture() {
	if d.capturing {
		return
	}
	d.capturing, d.capStart = true, d.pos
	d.capBuf = d.capBuf[:0]
}

// CaptureLen returns the number of bytes between StartCapture and the current
// input offset (the end of the last returned token).
func (d *Decoder) CaptureLen() int {
	if !d.capturing {
		return 0
	}
	return len(d.capBuf) + d.pos - d.capStart
}

// Captured returns a copy of bytes [from, to) of the recording.
func (d *Decoder) Captured(from, to int) []byte {
	out := make([]byte, 0, to-from)
	if from < len(d.capBuf) {
		out = append(out, d.capBuf[from:min(to, len(d.capBuf))]...)
	}
	if to > len(d.capBuf) {
		lo := max(from-len(d.capBuf), 0)
		out = append(out, d.buf[d.capStart+lo:d.capStart+to-len(d.capBuf)]...)
	}
	return out
}

// StopCapture ends the recording and releases its buffer.
func (d *Decoder) StopCapture() {
	d.capturing = false
	d.capBuf = d.capBuf[:0]
}
