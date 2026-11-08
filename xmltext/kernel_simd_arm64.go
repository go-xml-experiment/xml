// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.simd && arm64 && go1.27

package xmltext

import (
	"math/bits"
	"simd/archsimd"
)

const simdKernels = true

// plainPrefix returns the length of the leading run of bytes of b that need
// no attention from the text scanner: ASCII 0x20..0x7f other than '&' and
// ']'. NEON version: 16 bytes per step, exact stop position.
func plainPrefix(b []byte) int {
	lo := archsimd.BroadcastUint8x16(0x20)
	hi := archsimd.BroadcastUint8x16(0x7f)
	amp := archsimd.BroadcastUint8x16('&')
	rb := archsimd.BroadcastUint8x16(']')
	i := 0
	for ; i+16 <= len(b); i += 16 {
		x := archsimd.LoadUint8x16(b[i:])
		bad := x.Less(lo).Or(x.Greater(hi)).Or(x.Equal(amp)).Or(x.Equal(rb))
		w := bad.ToInt8x16().ConvertToUint8().ReshapeToUint64s()
		l, h := w.GetElem(0), w.GetElem(1)
		if l|h == 0 {
			continue
		}
		if l != 0 {
			return i + bits.TrailingZeros64(l)/8
		}
		return i + 8 + bits.TrailingZeros64(h)/8
	}
	return i
}

// escPrefix returns the length of a leading run of b whose bytes can be copied
// unchanged by the Encoder in every mode: ASCII 0x20..0x7f other than & < > "
// and '. NEON version with an exact stop position.
func escPrefix(b []byte) int {
	lo := archsimd.BroadcastUint8x16(0x20)
	hi := archsimd.BroadcastUint8x16(0x7f)
	amp := archsimd.BroadcastUint8x16('&')
	lt := archsimd.BroadcastUint8x16('<')
	gt := archsimd.BroadcastUint8x16('>')
	dq := archsimd.BroadcastUint8x16('"')
	sq := archsimd.BroadcastUint8x16('\'')
	i := 0
	for ; i+16 <= len(b); i += 16 {
		x := archsimd.LoadUint8x16(b[i:])
		bad := x.Less(lo).Or(x.Greater(hi)).Or(x.Equal(amp)).Or(x.Equal(lt)).Or(x.Equal(gt)).Or(x.Equal(dq)).Or(x.Equal(sq))
		w := bad.ToInt8x16().ConvertToUint8().ReshapeToUint64s()
		l, h := w.GetElem(0), w.GetElem(1)
		if l|h == 0 {
			continue
		}
		if l != 0 {
			return i + bits.TrailingZeros64(l)/8
		}
		return i + 8 + bits.TrailingZeros64(h)/8
	}
	return i
}
