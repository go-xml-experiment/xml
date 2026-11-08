// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import "testing"

// plain3 is a fast path for seqLen; it may never accept what seqLen rejects.
func TestPlain3AgreesWithSeqLen(t *testing.T) {
	for c := 0; c < 256; c++ {
		for b1 := 0; b1 < 256; b1 += 1 {
			for _, b2 := range []byte{0x00, 0x7f, 0x80, 0xa0, 0xbf, 0xc0} {
				b := []byte{byte(c), byte(b1), b2}
				if plain3(b) && seqLen(b) != 3 {
					t.Fatalf("plain3 accepts % x but seqLen=%d", b, seqLen(b))
				}
			}
		}
	}
}
