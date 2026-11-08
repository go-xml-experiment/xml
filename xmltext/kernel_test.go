// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"math/rand"
	"testing"
)

func plainExact(b []byte) int {
	for i, c := range b {
		if c < 0x20 || c > 0x7f || c == '&' || c == ']' {
			return i
		}
	}
	return len(b)
}

// plainPrefix must never run past a byte the scanner has to look at, and must
// not stop more than one vector early.
func TestPlainPrefix(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	special := []byte{0, 0x1f, 0x20, 0x7e, 0x7f, 0x80, 0xff, '&', ']', '<', '\n', '\t', '\r'}
	for iter := 0; iter < 200000; iter++ {
		n := rng.Intn(80)
		b := make([]byte, n)
		for i := range b {
			if rng.Intn(12) == 0 {
				b[i] = special[rng.Intn(len(special))]
			} else {
				b[i] = byte(0x21 + rng.Intn(0x5d))
			}
		}
		got, want := plainPrefix(b), plainExact(b)
		if got > want || want-got >= 16 {
			t.Fatalf("%q: plainPrefix=%d exact=%d", b, got, want)
		}
	}
	t.Logf("simd kernels: %v", simdKernels)
}

func escExact(b []byte) int {
	for i, c := range b {
		if c < 0x20 || c > 0x7f || c == '&' || c == '<' || c == '>' || c == '"' || c == '\'' {
			return i
		}
	}
	return len(b)
}

func TestEscPrefix(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	special := []byte{0, 0x1f, 0x20, 0x7e, 0x7f, 0x80, 0xff, '&', '<', '>', '"', '\'', '\n', '\t', '\r', ']'}
	for iter := 0; iter < 200000; iter++ {
		b := make([]byte, rng.Intn(80))
		for i := range b {
			if rng.Intn(12) == 0 {
				b[i] = special[rng.Intn(len(special))]
			} else {
				b[i] = byte(0x28 + rng.Intn(0x30))
			}
		}
		got, want := escPrefix(b), escExact(b)
		if got > want || simdKernels && want-got >= 16 { // the scalar version is conservative
			t.Fatalf("%q: escPrefix=%d exact=%d", b, got, want)
		}
	}
}
