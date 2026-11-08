// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package testenv holds the test helpers this module used from std's
// internal/testenv.
package testenv

import "testing"

// SkipIfOptimizationOff skips t when built with the noopt tag, because
// allocation counts are meaningless without compiler optimizations.
func SkipIfOptimizationOff(t testing.TB) {
	t.Helper()
	if optimizationOff {
		t.Skip("skipping test with optimization disabled")
	}
}
