// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !(goexperiment.simd && arm64 && go1.27)

package xmltext

import "encoding/binary"

// plainPrefix returns the length of the leading run of bytes of b that need
// no attention from the text scanner: ASCII 0x20..0x7f other than '&' and
// ']'. It may stop early (any multiple of 8 is allowed) but never past a
// byte that is not plain. This is the portable SWAR version.
func plainPrefix(b []byte) int {
	i := 0
	for ; i+8 <= len(b); i += 8 {
		if !plainWord(binary.LittleEndian.Uint64(b[i:])) {
			break
		}
	}
	return i
}

// escPrefix returns the length of a leading run of b whose bytes can be copied
// unchanged by the Encoder in every mode: ASCII 0x20..0x7f other than & < > "
// and '. It may stop early (a multiple of 8) but never past such a byte.
func escPrefix(b []byte) int {
	i := 0
	for ; i+8 <= len(b); i += 8 {
		if !escWordPlain(binary.LittleEndian.Uint64(b[i:])) {
			break
		}
	}
	return i
}

// escWordPlain is conservative: it also rejects '#', '=' and '?' (which share
// bit patterns with '"'/'&'/'\” and '<'/'>') so two zero-byte tests suffice.
func escWordPlain(w uint64) bool {
	if w&^lo7 != 0 { // high bit set
		return false
	}
	// bytes < 0x20: (b - 0x20) borrows into the high bit
	if (w-0x2020202020202020)&^w&0x8080808080808080 != 0 {
		return false
	}
	x := (w | 0x0505050505050505) ^ 0x2727272727272727 // 0x22 0x23 0x26 0x27
	if (x-ones)&^x&0x8080808080808080 != 0 {
		return false
	}
	x = (w | 0x0303030303030303) ^ 0x3F3F3F3F3F3F3F3F // 0x3C 0x3D 0x3E 0x3F
	return (x-ones)&^x&0x8080808080808080 == 0
}

const simdKernels = false
