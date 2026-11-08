// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"errors"
	"unsafe"

	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
)

// maxRetained is the largest buffer, in bytes, that Release keeps. A bigger
// one is dropped, as encoding/json/jsontext does when it recycles a Decoder,
// so that a single large document does not pin its buffers for as long as the
// value is kept for reuse.
const maxRetained = 64 << 10

var errReleased = errors.New("xmltext: use of released Decoder")

// keepBytes returns b emptied, or nil if it is larger than maxRetained.
func keepBytes(b []byte) []byte {
	if cap(b) > maxRetained {
		return nil
	}
	return b[:0]
}

// keepClear returns s emptied with every element zeroed, or nil if it is
// larger than maxRetained. Zeroing drops the strings the elements hold.
func keepClear[T any](s []T) []T {
	var z T
	if cap(s)*int(unsafe.Sizeof(z)) > maxRetained {
		return nil
	}
	clear(s[:cap(s)])
	return s[:0]
}

func sizeOf[T any](s []T) int {
	var z T
	return cap(s) * int(unsafe.Sizeof(z))
}

// Release drops everything d holds from its last use so that d can be kept for
// reuse without pinning that use's data: the input bytes or Reader, the
// options with their entity map, charset function and callbacks, the captured
// bytes, and the namespace and element-name tables. Buffers larger than 64 KiB
// are dropped, the rest are kept empty for the next Reset or ResetBytes. It
// returns the capacity in bytes of what is kept.
//
// The string cache set through Internal is left as is; its owner manages it.
// The Decoder's own string cache is kept empty.
//
// After Release d is unusable until Reset or ResetBytes; any other call
// returns an error. Tokens and views obtained before Release are invalid.
func (d *Decoder) Release() (retained int) {
	if d.ownBuf {
		d.buf = keepBytes(d.buf)
	} else {
		d.buf = nil // the caller's input
	}
	d.r, d.charsetPending, d.user = nil, nil, nil
	d.opts = xmlopts.Options{}
	d.legacy = nil
	d.rerr, d.deferredErr, d.engErr = nil, nil, nil
	d.err, d.rare = errReleased, true
	d.dropBatch()
	d.pos, d.eof, d.bi, d.bn, d.selfEnd, d.popEnd = 0, true, 0, 0, false, false
	d.kind = Invalid
	d.attrs = keepClearAttrSpans(d.attrs)
	d.scratch = keepBytes(d.scratch)
	d.attrBuf = keepBytes(d.attrBuf)
	d.nameArena = keepBytes(d.nameArena)
	d.stack = keepClear(d.stack)
	d.bindings = keepClear(d.bindings)
	d.nsMap, d.attrSet, d.expSet, d.defaultNS = nil, nil, nil, ""
	d.capturing = false
	clear(d.capBuf)
	d.capBuf = keepBytes(d.capBuf)
	if d.ownNames != nil {
		clear(d.ownNames[:])
		retained += int(unsafe.Sizeof(*d.ownNames))
	}
	return retained + sizeOf(d.buf) + sizeOf(d.attrs) + sizeOf(d.scratch) + sizeOf(d.attrBuf) +
		sizeOf(d.nameArena) + sizeOf(d.stack) + sizeOf(d.bindings) + sizeOf(d.capBuf)
}

// keepClearAttrSpans is keepClear for attrSpan, which holds no pointers.
func keepClearAttrSpans(s []attrSpan) []attrSpan {
	if sizeOf(s) > maxRetained {
		return nil
	}
	return s[:0]
}

// Release drops everything e holds from its last use so that e can be kept for
// reuse without pinning that use's data: the io.Writer, the strings of the
// names, namespaces and attributes written, and the indentation settings.
// Buffers larger than 64 KiB are dropped, the rest are kept empty for the next
// Reset. It returns the capacity in bytes of what is kept.
//
// After Release e is unusable until Reset; written bytes still buffered are
// discarded, so Flush or Close first.
func (e *Encoder) Release() (retained int) {
	e.attrSet = nil
	e.buf = keepBytes(e.buf)
	e.pbuf = keepBytes(e.pbuf)
	e.arena = keepBytes(e.arena)
	e.tattrs = keepClear(e.tattrs)
	e.stack = keepClear(e.stack)
	e.binds = keepClear(e.binds)
	e.autos = keepClear(e.autos)
	e.prefixes = keepClear(e.prefixes)
	e.akeys = keepClear(e.akeys)
	e.Reset(nil)
	return sizeOf(e.buf) + sizeOf(e.pbuf) + sizeOf(e.arena) + sizeOf(e.tattrs) +
		sizeOf(e.stack) + sizeOf(e.binds) + sizeOf(e.autos) + sizeOf(e.prefixes) + sizeOf(e.akeys)
}
