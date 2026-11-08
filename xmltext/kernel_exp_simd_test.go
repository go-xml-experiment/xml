// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build goexperiment.simd && arm64 && go1.27

package xmltext

import (
	"fmt"
	"math/bits"
	"simd/archsimd"
	"testing"
)

// tagStopSIMD is the rejected experiment: first '>', '"' or '\” in b with
// 16-byte NEON compares. Kept as a test-only reference for the crossover
// measurement below (it lost on real tags, whose runs are 5 to 40 bytes).
func tagStopSIMD(b []byte) int {
	gt := archsimd.BroadcastUint8x16('>')
	dq := archsimd.BroadcastUint8x16('"')
	sq := archsimd.BroadcastUint8x16('\'')
	i := 0
	for ; i+16 <= len(b); i += 16 {
		x := archsimd.LoadUint8x16(b[i:])
		w := x.Equal(gt).Or(x.Equal(dq)).Or(x.Equal(sq)).ToInt8x16().ConvertToUint8().ReshapeToUint64s()
		l, h := w.GetElem(0), w.GetElem(1)
		if l|h == 0 {
			continue
		}
		if l != 0 {
			return i + bits.TrailingZeros64(l)/8
		}
		return i + 8 + bits.TrailingZeros64(h)/8
	}
	for ; i < len(b) && !tagStop[b[i]]; i++ {
	}
	return i
}

func tagStopScalar(b []byte) int {
	i := 0
	for i < len(b) && !tagStop[b[i]] {
		i++
	}
	return i
}

// BenchmarkTagStopCrossover reports ns per search for runs of n plain bytes
// followed by '>'. NEON wins only from roughly one vector's length on.
func BenchmarkTagStopCrossover(b *testing.B) {
	for _, n := range []int{3, 8, 16, 32, 64, 256} {
		buf := make([]byte, n+1+16)
		for i := range buf {
			buf[i] = 'a'
		}
		buf[n] = '>'
		for name, f := range map[string]func([]byte) int{"scalar": tagStopScalar, "neon": tagStopSIMD} {
			b.Run(fmt.Sprintf("%s/run=%d", name, n), func(b *testing.B) {
				for range b.N {
					if f(buf) != n {
						b.Fatal("wrong")
					}
				}
			})
		}
	}
}
