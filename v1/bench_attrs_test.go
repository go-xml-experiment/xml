// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"io"
	"strconv"
	"strings"
	"testing"
)

// The attribute benchmarks unmarshal a document of 50 items whose start
// tags carry attributes, some of them not mapped to any field.

type attrBenchItem struct {
	ID    string   `xml:"id,attr"`
	Kind  string   `xml:"kind,attr"`
	Name  string   `xml:"name"`
	Price float64  `xml:"price"`
	Tags  []string `xml:"tags>tag"`
}

type attrBenchDoc struct {
	XMLName Name            `xml:"urn:shop order"`
	ID      string          `xml:"id,attr"`
	Items   []attrBenchItem `xml:"item"`
}

func attrBenchData() []byte {
	var sb strings.Builder
	sb.WriteString(`<order xmlns="urn:shop" xmlns:x="urn:x" id="o-1" x:rev="3">`)
	for i := range 50 {
		n := strconv.Itoa(i)
		sb.WriteString(`<item id="i-` + n + `" kind="k" x:note="unmapped" lang="en"><name>Item ` + n +
			`</name><price unit="EUR">9.5</price><tags><tag rel="a">x</tag><tag>y</tag></tags></item>`)
	}
	sb.WriteString(`</order>`)
	return []byte(sb.String())
}

func BenchmarkUnmarshalAttrs(b *testing.B) {
	data := attrBenchData()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var d attrBenchDoc
		if err := Unmarshal(data, &d); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTokenAttrs(b *testing.B) {
	data := attrBenchData()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		d := NewDecoder(bytes.NewReader(data))
		for {
			if _, err := d.Token(); err != nil {
				if err != io.EOF {
					b.Fatal(err)
				}
				break
			}
		}
	}
}
