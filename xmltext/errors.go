// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import "strconv"

// SyntaxError reports malformed XML at a position in the input.
type SyntaxError struct {
	Offset int64 // byte offset where the problem was detected
	Line   int   // 1-based
	Column int   // 1-based, in bytes
	Msg    string
}

func (e *SyntaxError) Error() string {
	return "XML syntax error on line " + strconv.Itoa(e.Line) + ", column " + strconv.Itoa(e.Column) + ": " + e.Msg
}
