// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bufio"
	"bytes"
	"io"
	"strings"
	"testing"
)

// Entity maps make every tokenizer Read observable, so byte-at-a-time and tiny
// readers used to cost input size x map size (see settings snapshots).
func entityBenchInput() []byte {
	return []byte("<r>" + strings.Repeat(`<item id="x">hello &amp; world</item>`, 20000) + "</r>")
}

func BenchmarkTokenV1BytesBufferHTMLEntity(b *testing.B) {
	data := entityBenchInput()
	kinds := []struct {
		name string
		make func() io.Reader
	}{
		{"bytes.Buffer", func() io.Reader { return bytes.NewBuffer(data) }},
		{"ByteReader", func() io.Reader { return plainByteReader{strings.NewReader(string(data))} }},
		{"bufio(16)", func() io.Reader { return bufio.NewReaderSize(bytes.NewReader(data), 16) }},
		{"bufio(4096)", func() io.Reader { return bufio.NewReader(bytes.NewReader(data)) }},
	}
	for _, k := range kinds {
		b.Run("v1/"+k.name, func(b *testing.B) {
			b.SetBytes(int64(len(data)))
			for range b.N {
				d := NewDecoder(k.make())
				d.Strict, d.Entity = false, HTMLEntity
				for {
					if _, err := d.Token(); err != nil {
						break
					}
				}
			}
		})
	}
}
