// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// Unlike the broad fuzz comparator, mutable-setting checks compare every token,
// error field and position, including the failing call and the following call.
func compareSettingsToken(t *testing.T, d *Decoder, raw bool) bool {
	t.Helper()
	var tk Token
	var err error
	if raw {
		tk, err = d.RawToken()
	} else {
		tk, err = d.Token()
	}
	line, col := d.InputPos()
	index := fixedCallIndex[t.Name()]
	if index == 0 {
		t.Cleanup(func() { delete(fixedCallIndex, t.Name()) })
	}
	fixedCallIndex[t.Name()] = index + 1
	want := fixedValue(t.Name(), "", index)
	got := []string{renderNew(tk), errString(err), fmt.Sprint(d.InputOffset(), ":", line, ":", col)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	return err != nil
}

func TestMutableDecoderSettings(t *testing.T) {
	cases := []struct {
		name, input    string
		before, change func(*Decoder)
	}{
		{"entity-nil-map", `<r>&thing;</r>`, nil, func(d *Decoder) {
			d.Entity = map[string]string{"thing": "ok"}
		}},
		{"entity-replace", `<r>&thing;</r>`, func(d *Decoder) {
			d.Entity = map[string]string{"thing": "old"}
		}, func(d *Decoder) {
			d.Entity = map[string]string{"thing": "new"}
		}},
		{"entity-in-place", `<r>&thing;</r>`, func(d *Decoder) {
			d.Entity = map[string]string{"thing": "old"}
		}, func(d *Decoder) {
			d.Entity["thing"] = "new"
		}},
		{"entity-map-nil", `<r>&thing;</r>`, func(d *Decoder) {
			d.Entity = map[string]string{"thing": "old"}
		}, func(d *Decoder) { d.Entity = nil }},
		{"strict-to-loose", `<r><x a=value/></r>`, nil, func(d *Decoder) { d.Strict = false }},
		{"loose-to-strict", `<r><x a=value/></r>`, func(d *Decoder) { d.Strict = false }, func(d *Decoder) { d.Strict = true }},
		{"autoclose-replace", `<r><x><y/></r>`, func(d *Decoder) { d.Strict = false }, func(d *Decoder) { d.AutoClose = []string{"x"} }},
		{"autoclose-in-place", `<r><x><y/></r>`, func(d *Decoder) {
			d.Strict = false
			d.AutoClose = []string{"unused"}
		}, func(d *Decoder) { d.AutoClose[0] = "x" }},
		{"default-space", `<r><x/></r>`, nil, func(d *Decoder) { d.DefaultSpace = "urn:new" }},
		{"charset-nil-func", `<!--before--><?xml encoding="custom"?><r/>`, nil, func(d *Decoder) {
			fn := func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
			d.CharsetReader = fn
		}},
		{"charset-func-nil", `<!--before--><?xml encoding="custom"?><r/>`, func(d *Decoder) {
			fn := func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
			d.CharsetReader = fn
		}, func(d *Decoder) { d.CharsetReader = nil }},
		{"charset-replace", `<!--before--><?xml encoding="custom"?><r/>`, func(d *Decoder) {
			fn := func(_ string, r io.Reader) (io.Reader, error) { return nil, fmt.Errorf("obsolete converter") }
			d.CharsetReader = fn
		}, func(d *Decoder) {
			fn := func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
			d.CharsetReader = fn
		}},
	}
	readers := []struct {
		name string
		new  func(string) io.Reader
	}{
		{"string", func(s string) io.Reader { return strings.NewReader(s) }},
		{"bytes", func(s string) io.Reader { return bytes.NewReader([]byte(s)) }},
		{"bufio", func(s string) io.Reader { return bufio.NewReader(strings.NewReader(s)) }},
	}
	for _, c := range cases {
		for _, r := range readers {
			for _, raw := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/raw=%v", c.name, r.name, raw), func(t *testing.T) {
					d := NewDecoder(r.new(c.input))
					if c.before != nil {
						c.before(d)
					}
					if compareSettingsToken(t, d, raw) {
						t.Fatal("first token failed")
					}
					c.change(d)
					for range 30 {
						if compareSettingsToken(t, d, raw) {
							compareSettingsToken(t, d, raw)
							return
						}
					}
					t.Fatal("decoder did not terminate")
				})
			}
		}
	}
}

func TestMutableDecoderSettingsConcreteTokens(t *testing.T) {
	d := NewDecoder(strings.NewReader(`<r>&thing;</r>`))
	if tok, err := d.Token(); err != nil || !reflect.DeepEqual(tok, StartElement{Name: Name{Local: "r"}, Attr: []Attr{}}) {
		t.Fatalf("first token: %#v, %v", tok, err)
	}
	d.Entity = map[string]string{"thing": "ok"}
	if tok, err := d.Token(); err != nil {
		t.Fatal(err)
	} else if text, ok := tok.(CharData); !ok || string(text) != "ok" {
		t.Fatalf("entity: %#v", tok)
	}
	d = NewDecoder(strings.NewReader(`<r><x a=value/></r>`))
	if tok, err := d.Token(); err != nil || !reflect.DeepEqual(tok, StartElement{Name: Name{Local: "r"}, Attr: []Attr{}}) {
		t.Fatalf("first token: %#v, %v", tok, err)
	}
	d.Strict = false
	want := StartElement{Name: Name{Local: "x"}, Attr: []Attr{{Name: Name{Local: "a"}, Value: "value"}}}
	if tok, err := d.Token(); err != nil || !reflect.DeepEqual(tok, want) {
		t.Fatalf("attribute: %#v, %v", tok, err)
	}
}

func TestMutableDecoderSettingsAfterLongText(t *testing.T) {
	for _, size := range []int{20 << 10, 40 << 10} {
		for _, entity := range []bool{false, true} {
			for _, buffered := range []bool{false, true} {
				for _, raw := range []bool{false, true} {
					t.Run(fmt.Sprintf("size=%d/entity=%v/buffered=%v/raw=%v", size, entity, buffered, raw), func(t *testing.T) {
						suffix := `<x a=value/></r>`
						if entity {
							suffix = `<x a="&thing;"/></r>`
						}
						input := `<r>` + strings.Repeat("x", size) + suffix
						var gr io.Reader = strings.NewReader(input)
						if buffered {
							gr = bufio.NewReader(gr)
						}
						d := NewDecoder(gr)
						if entity {
							d.Entity = map[string]string{"thing": "old"}
						}
						// Read root and the entire CharData. The next public call must
						// refresh settings even if coalescing peeked its start tag.
						for range 2 {
							if compareSettingsToken(t, d, raw) {
								t.Fatal("root or text failed")
							}
						}
						if entity {
							d.Entity = map[string]string{"thing": "new"}
						} else {
							d.Strict = false
						}
						for range 4 { // x start, x end, r end, EOF
							compareSettingsToken(t, d, raw)
						}
						compareSettingsToken(t, d, raw)
					})
				}
			}
		}
	}
}
