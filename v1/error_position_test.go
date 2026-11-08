// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestQuotedAttributeErrorPositionAgainstLegacy(t *testing.T) {
	inputs := []string{
		`<t a='<'>`,
		"<t a=\"x\n<\">",
		`<r/><t a='` + strings.Repeat("x", 4096) + `<'>`,
		`<t a='<`, // truncated attributes take the same lexer error path
	}
	kinds := append([]readerKind(nil), readerKinds...)
	kinds = append(kinds, readerKind{"Reader", func(in string) (io.Reader, remaining) {
		r := strings.NewReader(in)
		return struct{ io.Reader }{r}, r.Len
	}})
	for i, input := range inputs {
		for _, kind := range kinds {
			for _, strict := range []bool{true, false} {
				for _, raw := range []bool{false, true} {
					t.Run(fmt.Sprintf("%d/%s/strict=%v/raw=%v", i, kind.name, strict, raw), func(t *testing.T) {
						newReader, _ := kind.make(input)
						d := NewDecoder(newReader)
						d.Strict = strict
						for calls := 0; calls < 5; calls++ {
							if compareSettingsToken(t, d, raw) {
								compareSettingsToken(t, d, raw)
								return
							}
						}
						t.Fatal("expected quoted attribute error")
					})
				}
			}
		}
	}
}
