// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"testing"

	"github.com/go-xml-experiment/xml/xmltext"
)

// The small-document benchmarks measure what a call costs before it has much
// to parse or write: the ~230-byte order below has two items, attributes and
// nested tags. They cover the fixed per-call costs that the pooling of Unmarshal
// and Marshal and the small first Reader buffer remove.

type smallItem struct {
	ID    string   `xml:"id,attr"`
	Name  string   `xml:"name"`
	Price float64  `xml:"price"`
	Tags  []string `xml:"tags>tag"`
}

type smallOrder struct {
	XMLName Name        `xml:"order"`
	ID      string      `xml:"id,attr"`
	Items   []smallItem `xml:"item"`
}

var smallDoc = []byte(`<order id="o-1"><item id="a"><name>Widget</name><price>9.5</price><tags><tag>x</tag><tag>y</tag></tags></item><item id="b"><name>Gadget</name><price>12</price><tags><tag>z</tag></tags></item></order>`)

func BenchmarkSmallUnmarshalV1(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var o smallOrder
		if err := Unmarshal(smallDoc, &o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSmallUnmarshalParallelV1(b *testing.B) {
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			var o smallOrder
			if err := Unmarshal(smallDoc, &o); err != nil {
				b.Error(err)
				return
			}
		}
	})
}

func BenchmarkSmallDecodeReaderV1(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		var o smallOrder
		if err := NewDecoder(bytes.NewReader(smallDoc)).Decode(&o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSmallMarshalV1(b *testing.B) {
	var o smallOrder
	if err := Unmarshal(smallDoc, &o); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(&o); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSmallXmltextNewDecoderBytes(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		d := xmltext.NewDecoderBytes(smallDoc)
		for {
			if _, err := d.ReadToken(); err != nil {
				break
			}
		}
	}
}

func BenchmarkSmallXmltextNewDecoderReader(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		d := xmltext.NewDecoder(bytes.NewReader(smallDoc))
		for {
			if _, err := d.ReadToken(); err != nil {
				break
			}
		}
	}
}

// smallNamesDoc is an order of eight items, about 1.2 KB. Reading its start
// tag and attribute names makes 58 names, more than a new Decoder made
// before it allocated a name cache.
var smallNamesDoc = []byte(`<order id="o-1">` +
	string(bytes.Repeat([]byte(`<item id="a"><name>Widget</name><price>9.5</price><tags><tag>x</tag><tag>y</tag></tags></item>`), 8)) +
	`</order>`)

func BenchmarkSmallXmltextNewDecoderNames(b *testing.B) {
	b.ReportAllocs()
	b.SetBytes(int64(len(smallNamesDoc)))
	for b.Loop() {
		d := xmltext.NewDecoderBytes(smallNamesDoc)
		for {
			t, err := d.ReadToken()
			if err != nil {
				break
			}
			if t.Kind() == xmltext.StartElement {
				_ = t.RawName()
				for a := t.Attrs(); a.Next(); {
					_ = a.RawName()
				}
			}
		}
	}
}
