// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"io"
	"os"
	"testing"
)

var consumerBytes int

func BenchmarkConsumer(b *testing.B) {
	doc, err := os.ReadFile(os.Getenv("XMLFILE"))
	if err != nil {
		b.Skip()
	}
	d := NewDecoderBytes(doc)
	for b.Loop() {
		d.ResetBytes(doc)
		n := 0
		for {
			t, err := d.ReadToken()
			if err != nil {
				if err != io.EOF {
					b.Fatal(err)
				}
				break
			}
			switch t.Kind() {
			case StartElement:
				n += len(t.Name().Local)
				for it := t.Attrs(); it.Next(); {
					n += len(it.Name().Local) + len(it.Value())
				}
			case EndElement:
				n += len(t.Name().Local)
			case Text:
				n += len(t.Text())
			}
		}
		consumerBytes = n
	}
}
