// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

func TestMemSourceWindow(t *testing.T) {
	const prefix = "skipped:"
	input := prefix + "<r/>" + strings.Repeat("x", 16<<20)
	for _, rk := range readerKinds[:2] {
		t.Run(rk.name, func(t *testing.T) {
			r, rem := rk.make(input)
			seeker := r.(io.Seeker)
			if _, err := seeker.Seek(int64(len(prefix)), io.SeekStart); err != nil {
				t.Fatal(err)
			}
			s := newSource(r)
			p := make([]byte, 8192)
			n, err := s.reader().Read(p)
			if err != nil || n < 4 || string(p[:n]) != input[len(prefix):len(prefix)+n] {
				t.Fatalf("Read = %d, %v, prefix %q", n, err, p[:4])
			}
			if got, want := rem(), len(input)-len(prefix); got != want {
				t.Fatalf("read-ahead moved caller: remaining %d, want %d", got, want)
			}
			s.commit(4, false)
			if got, want := rem(), len(input)-len(prefix)-4; got != want {
				t.Fatalf("commit left %d bytes, want %d", got, want)
			}
		})
	}
}

func TestMemSourceInitialOffsetAgainstLegacy(t *testing.T) {
	const prefix = "not XML:"
	inputs := []string{
		"", "<r/>", "<r>text</r>tail", "text", "<r>", "<r a=\"unfinished",
		"<r>" + strings.Repeat("x", 4092) + "</r>",
		"<r>" + strings.Repeat("x", 4093) + "</r>",
		"<r>" + strings.Repeat("x", 32766) + "</r>tail",
		"<r>" + strings.Repeat("x", 1<<20) + "</r>tail",
		"<r>" + strings.Repeat("한글\r\n&amp;", 1<<16) + "</r>tail",
		"<" + strings.Repeat("n", 5000) + "/>",
		"<r a=\"" + strings.Repeat("x", 9000) + "\"/>",
		"<r><!--" + strings.Repeat("x", 9000) + "--></r>",
		"<r><![CDATA[" + strings.Repeat("x", 9000) + "]]></r>",
	}
	for _, rk := range readerKinds[:2] {
		for i, input := range inputs {
			t.Run(fmt.Sprintf("%s/%d", rk.name, i), func(t *testing.T) {
				shifted := readerKind{name: rk.name, make: func(in string) (io.Reader, remaining) {
					r, rem := rk.make(prefix + in)
					if _, err := r.(io.Seeker).Seek(int64(len(prefix)), io.SeekStart); err != nil {
						t.Fatal(err)
					}
					return r, rem
				}}
				want := fixedValue("positions", input, 0)
				got, gerr := positions(input, shifted)
				if traceDigest(got) != want[0] || fmt.Sprint(gerr) != want[1] {
					t.Fatalf("legacy: %q (%v)\n   new: %q (%v)", want[0], want[1], got, gerr)
				}
			})
		}
	}
}

func TestMemReaderCoalescesEntityText(t *testing.T) {
	const count = 1 << 14
	input := "<r>x" + strings.Repeat("&amp;", count) + "</r>tail"
	want := "x" + strings.Repeat("&", count)
	for _, rk := range readerKinds[:2] {
		t.Run(rk.name, func(t *testing.T) {
			r, remaining := rk.make(input)
			d := NewDecoder(r)
			if _, err := d.Token(); err != nil {
				t.Fatal(err)
			}
			token, err := d.Token()
			text, ok := token.(CharData)
			if err != nil || !ok || string(text) != want {
				t.Fatalf("text kind=%T bytes=%d, want %d; error=%v", token, len(text), len(want), err)
			}
			if got, want := remaining(), len("/r>tail"); got != want {
				t.Fatalf("remaining=%d, want %d", got, want)
			}
			if token, err := d.Token(); err != nil || token != (EndElement{Name{Local: "r"}}) {
				t.Fatalf("end token=%v, error=%v", token, err)
			}
			if token, err := d.Token(); err != nil || string(token.(CharData)) != "tail" {
				t.Fatalf("tail token=%v, error=%v", token, err)
			}
		})
	}
}

func TestMemSourceCharsetRemainder(t *testing.T) {
	const prefix = "skipped:"
	const declaration = `<?xml version="1.0" encoding="window-test"?>`
	body := "<r>" + strings.Repeat("x", 9000) + "</r>"
	for _, rk := range readerKinds[:2] {
		t.Run(rk.name, func(t *testing.T) {
			r, _ := rk.make(prefix + declaration + body)
			if _, err := r.(io.Seeker).Seek(int64(len(prefix)), io.SeekStart); err != nil {
				t.Fatal(err)
			}
			d := NewDecoder(r)
			calls := 0
			d.CharsetReader = func(label string, input io.Reader) (io.Reader, error) {
				calls++
				if label != "window-test" {
					t.Fatalf("charset = %q", label)
				}
				remainder, err := io.ReadAll(input)
				if err != nil {
					return nil, err
				}
				if string(remainder) != body {
					t.Fatalf("charset remainder length = %d, want %d", len(remainder), len(body))
				}
				return bytes.NewReader(remainder), nil
			}
			var text []byte
			for {
				token, err := d.Token()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if data, ok := token.(CharData); ok {
					text = append(text, data...)
				}
			}
			if calls != 1 || string(text) != strings.Repeat("x", 9000) {
				t.Fatalf("charset calls = %d, decoded text length = %d", calls, len(text))
			}
		})
	}
}

func BenchmarkMemReaderFirstToken(b *testing.B) {
	for _, size := range []int{1 << 20, 16 << 20} {
		input := "<r/>" + strings.Repeat("x", size-4)
		for _, rk := range readerKinds[:2] {
			b.Run(fmt.Sprintf("%s/%dMiB", rk.name, size>>20), func(b *testing.B) {
				// Build the caller-owned input before measuring decoder allocations.
				var makeReader func() io.Reader
				if rk.name == "bytes.Reader" {
					data := []byte(input)
					makeReader = func() io.Reader { return bytes.NewReader(data) }
				} else {
					makeReader = func() io.Reader { return strings.NewReader(input) }
				}
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					if _, err := NewDecoder(makeReader()).Token(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
