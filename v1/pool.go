// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"math/bits"
	"sync"
	"unsafe"

	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
)

// Unmarshal, Marshal and MarshalIndent reuse state only while it remains private.
// Before invoking an UnmarshalXML or MarshalXML hook, the state is marked exposed:
// the hook may retain its Decoder or Encoder, so it must not be reset or pooled.
// NewDecoder and NewEncoder are never pooled.
//
// State returned to a pool drops references to caller data and settings, and
// discards buffers larger than maxPooled.

// maxPooled is the largest buffer, in bytes, that a pooled Decoder or Encoder
// keeps. It is the limit encoding/json/v2 uses for its pooled buffers.
const maxPooled = 64 << 10

// maxPooledNS is the most namespace bindings a pooled Decoder's map may have
// held at once. A map keeps its buckets after clear, so one that has grown
// past this is dropped.
const maxPooledNS = 64

// maxPooledStack is the most stack records a pooled Decoder keeps for reuse.
const maxPooledStack = 64

var decoderPool sync.Pool // of *Decoder

// getDecoder returns a Decoder reading data. Its settings are those of
// NewDecoder.
func getDecoder(data []byte) *Decoder {
	d, _ := decoderPool.Get().(*Decoder)
	if d == nil {
		d = NewDecoder(nil)
	}
	if data == nil {
		data = []byte{}
	}
	d.data = data
	return d
}

// putDecoder gives d back to the pool after dropping what the call left in it.
// It returns the size in bytes of the buffers d keeps.
func putDecoder(d *Decoder) (retained int) {
	if d.hookExposed {
		return 0
	}
	// The caller's data and what the tokens and errors refer to.
	d.t, d.src, d.data, d.in = nil, nil, nil, nil
	d.next, d.err, d.toClose = pendingToken{}, nil, Name{}
	d.needClose, d.saving, d.inUnmarshalXML = false, false, false
	// The public settings: UnmarshalXML can change them mid-call.
	d.Strict, d.AutoClose, d.Entity, d.CharsetReader, d.DefaultSpace = true, nil, nil, nil, ""
	d.settings = xmlopts.LegacyDecoder{}
	// The tokenizer.
	if d.xd != nil {
		retained += d.xd.Release()
		d.xd, d.spare = nil, d.xd
	} else if d.spare != nil {
		retained += d.spare.Release()
	}
	// The namespace map.
	if d.nsPeak > maxPooledNS {
		d.ns = make(map[string]string)
	} else {
		clear(d.ns)
	}
	d.nsPeak = 0
	// The stack: a few records are recycled through d.free, the rest is left
	// to the GC.
	var free *stack
	n := 0
	for _, list := range [2]*stack{d.stk, d.free} {
		for s := list; s != nil && n < maxPooledStack; n++ {
			next := s.next
			*s = stack{next: free}
			free = s
			s = next
		}
	}
	d.free, d.stk, d.stkDepth = free, nil, 0
	decoderPool.Put(d)
	return retained
}

// encoderState is a pooled Encoder with the buffer it writes to.
type encoderState struct {
	enc *Encoder
	buf bytes.Buffer

	bufStats bufferStatistics
}

// bufferStatistics tracks how well the output buffer is used, to decide
// whether to keep it; see putEncoder. It is the policy of
// encoding/json/jsontext for its buffered encoders (go.dev/issue/27735).
type bufferStatistics struct {
	strikes int // number of times the buffer was under-utilized
	prevLen int // length of previous buffer
}

var encoderPool sync.Pool // of *encoderState

// getEncoder returns an Encoder that writes to an empty buffer.
func getEncoder() *encoderState {
	st, _ := encoderPool.Get().(*encoderState)
	if st == nil {
		st = new(encoderState)
		st.enc = NewEncoder(&st.buf)
		return st
	}
	st.reset()
	return st
}

// reset prepares a pooled st for another call.
func (st *encoderState) reset() {
	if st.buf.Cap() == 0 && st.bufStats.prevLen > 0 {
		// Round up to a power of two, at least 64, as jsontext does.
		st.buf.Grow(1 << bits.Len(uint(st.bufStats.prevLen|63)))
	}
	st.enc.p.enc.Reset(&st.buf, xmlopts.LegacyOutput(true))
}

// result returns the output as a slice that the caller owns, nil if empty.
// An exposed encoder or oversized buffer will not be reused, so its output
// needs no copy.
func (st *encoderState) result() []byte {
	switch {
	case st.buf.Len() == 0:
		return nil
	case st.enc.p.hookExposed || st.buf.Cap() > maxPooled:
		return st.buf.Bytes()
	}
	return bytes.Clone(st.buf.Bytes())
}

// putEncoder gives st back to the pool after dropping what the call left in
// it. It returns the size in bytes of the buffers st keeps.
func putEncoder(st *encoderState) (retained int) {
	p := &st.enc.p
	if p.hookExposed {
		return 0
	}
	retained += p.enc.Release()
	clear(p.tags[:cap(p.tags)])
	p.tags, p.closed = p.tags[:0], false
	// Keep a buffer larger than 4 KiB only if it is sufficiently used: one
	// that is under a quarter full four times in a row is dropped, so a
	// single large output is not kept alive by a stream of small ones. The
	// worst case utilization is thus (25%)/(1+4) = 5%. Above maxPooled a
	// buffer is never kept.
	switch c, n := st.buf.Cap(), st.buf.Len(); {
	case c > maxPooled:
		st.bufStats = bufferStatistics{}
		st.buf = bytes.Buffer{}
	case c <= 4<<10, c/4 <= n:
		st.bufStats.strikes = 0
		st.buf.Reset()
	case st.bufStats.strikes < 4:
		st.bufStats.strikes++
		st.buf.Reset()
	default:
		st.bufStats = bufferStatistics{prevLen: n}
		st.buf = bytes.Buffer{}
	}
	retained += st.buf.Cap() + cap(p.tags)*int(unsafe.Sizeof(Name{}))
	encoderPool.Put(st)
	return retained
}
