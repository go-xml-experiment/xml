// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"strings"
	"testing"
)

func benchDocs() map[string][]byte {
	tag := strings.Repeat(`<item id="1" name="abc" kind="x"><v>1</v><v>2</v></item>`+"\n", 20000)
	ent := strings.Repeat(`<t>a &amp; b &lt; c &#x41; d &quot;</t>`+"\n", 25000)
	ascii := strings.Repeat("The quick brown fox jumps over the lazy dog. ", 25000)
	kor := strings.Repeat("가나다라마바사아자차카타파하 ", 60000)
	korTags := strings.Repeat(`<항목 번호="1" 이름="가나다"><값>라마바</값></항목>`+"\n", 20000)
	attrRefs := strings.Repeat(`<a href="/p?x=1&amp;y=2" title="A &quot;B&quot; C">t</a>`+"\n", 20000)
	return map[string][]byte{
		"KoreanTags":  []byte("<r>" + korTags + "</r>"),
		"AttrRefs":    []byte("<r>" + attrRefs + "</r>"),
		"TagHeavy":    []byte("<r>" + tag + "</r>"),
		"EntityHeavy": []byte("<r>" + ent + "</r>"),
		"ASCII":       []byte("<r>" + ascii + "</r>"),
		"Korean":      []byte("<r>" + kor + "</r>"),
	}
}

func BenchmarkReadToken(b *testing.B) {
	for name, doc := range benchDocs() {
		b.Run(name+"/xmltext-bytes", func(b *testing.B) {
			b.SetBytes(int64(len(doc)))
			b.ReportAllocs()
			d := NewDecoderBytes(doc)
			for b.Loop() {
				d.ResetBytes(doc)
				sum := 0
				for {
					t, err := d.ReadToken()
					if err != nil {
						break
					}
					switch t.Kind() {
					case StartElement:
						sum += len(t.Name().Local)
						for it := t.Attrs(); it.Next(); {
							sum += len(it.Value())
						}
					case Text:
						sum += len(t.Text())
					}
				}
				_ = sum
			}
		})
		b.Run(name+"/xmltext-reader", func(b *testing.B) {
			b.SetBytes(int64(len(doc)))
			b.ReportAllocs()
			d := NewDecoder(bytes.NewReader(doc))
			for b.Loop() {
				d.Reset(bytes.NewReader(doc))
				for {
					if _, err := d.ReadToken(); err != nil {
						break
					}
				}
			}
		})
	}
}

// BenchmarkAttrNames reads attribute names (resolved), the path a namespace
// declaration check on AttrIter.Name would slow down.
func BenchmarkAttrNames(b *testing.B) {
	doc := benchDocs()["TagHeavy"]
	b.SetBytes(int64(len(doc)))
	b.ReportAllocs()
	d := NewDecoderBytes(doc)
	for b.Loop() {
		d.ResetBytes(doc)
		sum := 0
		for {
			t, err := d.ReadToken()
			if err != nil {
				break
			}
			if t.Kind() == StartElement {
				for it := t.Attrs(); it.Next(); {
					sum += len(it.Name().Local)
				}
			}
		}
		_ = sum
	}
}
