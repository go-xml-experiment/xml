// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"io"
	"os"
	"testing"
)

func BenchmarkCorpus(b *testing.B) {
	name := os.Getenv("XMLFILE")
	if name == "" {
		b.Skip("set XMLFILE")
	}
	doc, err := os.ReadFile(name)
	if err != nil {
		b.Fatal(err)
	}
	d := NewDecoderBytes(doc)
	b.SetBytes(int64(len(doc)))
	for b.Loop() {
		d.ResetBytes(doc)
		for {
			if _, err := d.ReadToken(); err != nil {
				if err != io.EOF {
					b.Fatal(err)
				}
				break
			}
		}
	}
}
