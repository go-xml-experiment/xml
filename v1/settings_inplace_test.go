// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// onByteReader hands out one byte per Read and runs the callback registered for
// the offset right after that byte has been consumed.
type onByteReader struct {
	input string
	off   int
	at    map[int]func()
}

func (r *onByteReader) Read(p []byte) (int, error) {
	if r.off == len(r.input) {
		return 0, io.EOF
	}
	p[0] = r.input[r.off]
	r.off++
	if f := r.at[r.off]; f != nil {
		f()
	}
	return 1, nil
}

// An Entity map changed in place by the Reader stays visible in later tokens,
// also in a token where Strict changes in the middle (which replays settings).
func TestSettingsInPlaceEditSurvivesLaterChange(t *testing.T) {
	const input = `<r><a>&e;</a><b>&e;|&x;</b><c>&e;</c></r>`
	run := func(newDecoder func(io.Reader) (func() (string, error), func(map[string]string), func(bool))) []string {
		m := map[string]string{"e": "one"}
		src := &onByteReader{input: input, at: map[int]func(){}}
		next, setEntity, setStrict := newDecoder(src)
		setEntity(m)
		setStrict(true)
		src.at[strings.Index(input, "<a>")+3] = func() { m["e"] = "two" } // in place, same length
		src.at[strings.Index(input, "|")+1] = func() { setStrict(false) }
		var out []string
		for {
			s, err := next()
			if err != nil {
				return append(out, fmt.Sprint(err))
			}
			out = append(out, s)
		}
	}
	got := run(func(r io.Reader) (func() (string, error), func(map[string]string), func(bool)) {
		d := NewDecoder(r)
		return func() (string, error) { tk, err := d.Token(); return renderNew(tk), err },
			func(m map[string]string) { d.Entity = m }, func(b bool) { d.Strict = b }
	})
	want := []string{`S "" "r" ""`, `S "" "a" ""`, `T "two"`, `E "" "a"`, `S "" "b" ""`, `T "two|&x;"`, `E "" "b"`, `S "" "c" ""`, `T "two"`, `E "" "c"`, `E "" "r"`, "EOF"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy: %q\n   new: %q", want, got)
	}
	if !strings.Contains(strings.Join(got, ""), `T "two|&x;"`) || !strings.Contains(strings.Join(got, ""), `T "two"`) {
		t.Fatalf("the in-place edit was lost: %q", got)
	}
}

// Every Read looks at the settings; that must not depend on the size of the
// Entity map (it used to compare and copy it, so a byte-at-a-time Reader made
// the cost input size x map size: seconds for 740 KB with HTMLEntity).
func TestSettingsReadCostIndependentOfEntityMap(t *testing.T) {
	data := entityBenchInput()
	start := time.Now()
	for _, src := range []io.Reader{
		&onByteReader{input: string(data)},
		plainByteReader{strings.NewReader(string(data))},
	} {
		d := NewDecoder(src)
		d.Strict, d.Entity = false, HTMLEntity
		n := 0
		for {
			if _, err := d.Token(); err != nil {
				if err != io.EOF {
					t.Fatal(err)
				}
				break
			}
			n++
		}
		if n != 3*20000+2 {
			t.Fatalf("tokens = %d", n)
		}
	}
	if el := time.Since(start); el > 2*time.Second { // about 40 ms; 11 s before
		t.Errorf("two byte-at-a-time passes took %v", el)
	}
}
