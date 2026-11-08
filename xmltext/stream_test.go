// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestTextStreaming(t *testing.T) {
	unit := "한😀x\r\n&amp;&#65;]]x"
	rawText := strings.Repeat(unit, 60000) // >1 MiB, no markup until the closing tag
	want := strings.Repeat("한😀x\n&A]]x", 60000)
	input := "<r>" + rawText + "</r>"
	// SplitText: the first piece arrives long before the run ends.
	r := strings.NewReader(input)
	d := NewDecoder(r, SplitText(true))
	if _, err := d.ReadToken(); err != nil {
		t.Fatal(err)
	}
	first, err := d.ReadToken()
	if err != nil || first.Kind() != Text || !first.Partial() || r.Len() < len(input)/2 {
		t.Fatalf("first text waited for the whole run: %v, %d unread bytes", err, r.Len())
	}
	// Whole: the same read returns the entire run.
	r = strings.NewReader(input)
	d = NewDecoder(r)
	d.ReadToken()
	if whole, err := d.ReadToken(); err != nil || whole.Partial() || string(whole.Text()) != want {
		t.Fatalf("whole text: %v", err)
	}
	for _, split := range []bool{false, true} {
		for _, chunk := range []int{0, 1, 2, 3, 7, 16, 4096} {
			t.Run(fmt.Sprint("split=", split, "/chunk=", chunk), func(t *testing.T) {
				makeDecoder := func() *Decoder {
					opts := []Options{SplitText(split)}
					if chunk == 0 {
						return NewDecoderBytes([]byte(input), opts...)
					}
					return NewDecoder(&charsetChunkReader{r: strings.NewReader(input), n: chunk}, opts...)
				}
				d := makeDecoder()
				var text bytes.Buffer
				parts := 0
				for {
					tk, err := d.ReadToken()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatal(err)
					}
					if tk.Kind() == Text {
						text.Write(tk.Text())
						parts++
					}
				}
				if text.String() != want || split && parts < 2 || !split && parts != 1 {
					t.Fatalf("text differs or wrong pieces: %d parts, %d bytes", parts, text.Len())
				}
				if d.InputOffset() != int64(len(input)) {
					t.Fatalf("offset %d", d.InputOffset())
				}
				raw, err := makeDecoder().ReadElement()
				if err != nil || string(raw) != input {
					t.Fatalf("capture differs: %d bytes, %v", len(raw), err)
				}
				d = makeDecoder()
				if err := d.SkipElement(); err != nil {
					t.Fatal(err)
				}
				if _, err := d.ReadToken(); err != io.EOF {
					t.Fatalf("skip end: %v", err)
				}
			})
		}
	}
}

func TestTextStreamingLongReference(t *testing.T) {
	name := strings.Repeat("n", 40000)
	for _, ref := range []string{"&" + name + ";", "&#" + strings.Repeat("0", 40000) + "65;"} {
		in := "<r>" + ref + strings.Repeat("x", 70000) + "</r>"
		want := "A" + strings.Repeat("x", 70000)
		for _, split := range []bool{false, true} {
			for _, chunk := range []int{0, 1, 2, 3, 7, 16, 4096} {
				opts := []Options{Permissive(true), SplitText(split), WithEntities(map[string]string{name: "A"})}
				var d *Decoder
				if chunk == 0 {
					d = NewDecoderBytes([]byte(in), opts...)
				} else {
					d = NewDecoder(&charsetChunkReader{r: strings.NewReader(in), n: chunk}, opts...)
				}
				got, err := readAll(t, d)
				if err != nil || normalize(got)[1].text != want {
					t.Fatalf("long reference split %v chunk %d: %v", split, chunk, err)
				}
			}
		}
	}
}

func TestTextStreamingEdges(t *testing.T) {
	for _, edge := range []string{"&amp;", "&#x1F600;", "한", "😀", "\r\n", "]]>", "]]x", "&bogus;", "\xf0\x9f", "&"} {
		for split := 0; split <= len(edge); split++ {
			in := "<r>" + strings.Repeat("x", (32<<10)-split) + edge + "</r>"
			want, wantErr := expectedTokens(t, in)
			for _, splitText := range []bool{false, true} {
				for _, chunk := range []int{0, 1, 2, 3, 7, 16, 4096} {
					var d *Decoder
					if chunk == 0 {
						d = NewDecoderBytes([]byte(in), SplitText(splitText))
					} else {
						d = NewDecoder(&charsetChunkReader{r: strings.NewReader(in), n: chunk}, SplitText(splitText))
					}
					got, err := readAll(t, d)
					if (err == nil) != (wantErr == nil) {
						t.Fatalf("edge %q split %d chunk %d SplitText %v: %v, oracle %v", edge, split, chunk, splitText, err, wantErr)
					}
					if err == nil && fmt.Sprint(normalize(got)) != fmt.Sprint(normalize(want)) {
						t.Fatalf("edge %q split %d chunk %d SplitText %v: decoded text differs", edge, split, chunk, splitText)
					}
				}
			}
		}
	}
}

func TestMaxTokenSizeStreaming(t *testing.T) {
	for _, chunk := range []int{0, 1, 7, 4096} {
		for _, c := range []struct {
			in   string
			max  int
			fail bool
		}{
			{"<r>" + strings.Repeat("x", 100000) + "</r>", 16, false},
			{"<r>1234567890</r>", 4, false},
			{`<r x="12345"/>`, 13, true},
			{`<r x="12345"/>`, 14, false},
			{`<r><!--123456789--></r>`, 16, false},
			{`<r><!--1234567890--></r>`, 16, true},
			{`<r>&#0000000000000000065;</r>`, 16, true},
			{`<r>&amp;</r>`, 4, true},
			{`<r>&amp;</r>`, 5, false},
		} {
			var d *Decoder
			opts := []Options{Permissive(true), SplitText(true), MaxTokenSize(c.max)}
			if chunk == 0 {
				d = NewDecoderBytes([]byte(c.in), opts...)
			} else {
				d = NewDecoder(&charsetChunkReader{r: strings.NewReader(c.in), n: chunk}, opts...)
			}
			got, err := readAll(t, d)
			if (err != nil) != c.fail {
				t.Fatalf("chunk %d max %d %q: %v", chunk, c.max, c.in, err)
			}
			if err != nil && !strings.Contains(err.Error(), "MaxTokenSize") {
				t.Fatalf("wrong limit error: %v", err)
			}
			if !c.fail {
				want, _ := expectedTokens(t, c.in)
				if fmt.Sprint(normalize(got)) != fmt.Sprint(normalize(want)) {
					t.Fatalf("bounded text differs: %v", got)
				}
			}
		}
	}
}
