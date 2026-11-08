// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build !race

// Package race reports whether the race detector is enabled.
package race

// Enabled is true when the program is built with -race.
const Enabled = false
