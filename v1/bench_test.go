// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// The 16 MiB catalog of the cross-runtime benchmarks, decoded into structs.
type benchChapter struct {
	Number string `xml:"number,attr"`
	Text   string `xml:",chardata"`
}
type benchBook struct {
	ID          string         `xml:"id,attr"`
	Lang        string         `xml:"lang,attr"`
	Code        string         `xml:"code,attr"`
	Title       string         `xml:"title"`
	Author      string         `xml:"author"`
	Description string         `xml:"description"`
	Chapters    []benchChapter `xml:"chapter"`
}
type benchCatalog struct {
	Books []benchBook `xml:"book"`
}

func benchData(b *testing.B) []byte {
	name := os.Getenv("XMLFILE")
	if name == "" {
		b.Skip("set XMLFILE (e.g. the stax-xml runtime-comparison-16mib.xml)")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		b.Fatal(err)
	}
	return data
}

func BenchmarkUnmarshalV1OnXMLText(b *testing.B) {
	data := benchData(b)
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		var c benchCatalog
		if err := Unmarshal(data, &c); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTokenV1OnXMLText(b *testing.B) {
	data := benchData(b)
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

func marshalFixture(b *testing.B, data []byte) benchCatalog {
	var c benchCatalog
	if err := Unmarshal(data, &c); err != nil {
		b.Fatal(err)
	}
	return c
}

func BenchmarkMarshalV1OnXMLText(b *testing.B) {
	data := benchData(b)
	c := marshalFixture(b, data)
	out, _ := Marshal(c)
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Marshal(c); err != nil {
			b.Fatal(err)
		}
	}
}
