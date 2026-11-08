// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

func TestNameProfiles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		legacy bool
	}{
		{"\u0132", false},
		{"\u0140", false},
		{"\u2070", false},
		{"\U00010000", false},
		{"é", true},
		{"한", true},
		{"a\u0300", true},
	} {
		t.Run(fmt.Sprintf("%U", []rune(tc.name)), func(t *testing.T) {
			for _, profile := range []struct {
				name string
				opts []Options
				want bool
			}{
				{"strict", nil, true},
				{"permissive", []Options{Permissive(true)}, tc.legacy},
				{"loose", []Options{Loose(true)}, tc.legacy},
			} {
				for _, in := range []string{
					"<" + tc.name + "/>",
					"<r " + tc.name + "=\"v\"/>",
					"<?" + tc.name + "?><r/>",
				} {
					for _, stream := range []bool{false, true} {
						d := NewDecoderBytes([]byte(in), profile.opts...)
						if stream {
							d = NewDecoder(iotest.OneByteReader(strings.NewReader(in)), profile.opts...)
						}
						_, err := readAll2(d)
						if (err == nil) != profile.want {
							t.Errorf("%s stream=%v input=%q: err=%v, want accepted=%v", profile.name, stream, in, err, profile.want)
						}
					}
				}
			}
			for _, strict := range []bool{true, false} {
				cfg := compatibilityCfg{strict: strict, entity: map[string]string{tc.name: "replacement"}}
				for _, in := range []string{"<r>&" + tc.name + ";</r>", "<r k=\"&" + tc.name + ";\"/>"} {
					if diff := diffExpected(t, in, cfg); diff != "" {
						t.Errorf("entity strict=%v input=%q: %s", strict, in, diff)
					}
				}
			}
			_, err := readAll2(NewDecoderBytes([]byte("<r>&"+tc.name+";</r>"), WithEntities(map[string]string{tc.name: "replacement"})))
			if err != nil {
				t.Errorf("strict fifth-edition entity rejected: %v", err)
			}
		})
	}
}

func TestEntityNameProfiles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"1x", false},
		{"a b", false},
		{"ok", true},
		{"é", true},
	} {
		for _, profile := range []struct {
			name  string
			opts  []Options
			loose bool
		}{
			{"strict", nil, false},
			{"permissive", []Options{Permissive(true)}, false},
			{"loose", []Options{Loose(true)}, true},
		} {
			opts := append(profile.opts, WithEntities(map[string]string{tc.name: "replacement"}))
			for _, in := range []string{"<r>&" + tc.name + ";</r>", "<r k=\"&" + tc.name + ";\"/>"} {
				for _, stream := range []bool{false, true} {
					d := NewDecoderBytes([]byte(in), opts...)
					if stream {
						d = NewDecoder(iotest.OneByteReader(strings.NewReader(in)), opts...)
					}
					got, err := readAll2(d)
					if !tc.valid && !profile.loose {
						if err == nil || !strings.Contains(err.Error(), "invalid character entity") {
							t.Errorf("%s stream=%v input=%q: want entity error, got %v", profile.name, stream, in, err)
						}
						continue
					}
					want := "replacement"
					if !tc.valid {
						want = "&" + tc.name + ";"
					}
					var value string
					for _, tk := range got {
						value += tk.text
						for _, a := range tk.attrs {
							value += a.Value
						}
					}
					if err != nil || value != want {
						t.Errorf("%s stream=%v input=%q: value=%q err=%v, want %q", profile.name, stream, in, value, err, want)
					}
				}
			}
		}
	}
}

func TestTruncatedNameProfiles(t *testing.T) {
	for _, profile := range []struct {
		name string
		opts []Options
		want string
	}{
		{"strict", nil, "unexpected EOF"},
		{"permissive", []Options{Permissive(true)}, "invalid XML name: \u0132"},
		{"loose", []Options{Loose(true)}, "invalid XML name: \u0132"},
	} {
		for _, in := range []string{"<r></\u0132 x", "<?\u0132 x"} {
			for _, stream := range []bool{false, true} {
				d := NewDecoderBytes([]byte(in), profile.opts...)
				if stream {
					d = NewDecoder(iotest.OneByteReader(strings.NewReader(in)), profile.opts...)
				}
				_, err := readAll2(d)
				if se, ok := err.(*SyntaxError); !ok || se.Msg != profile.want {
					t.Errorf("%s stream=%v input=%q: err=%v, want %q", profile.name, stream, in, err, profile.want)
				}
			}
		}
	}
}

func TestEncoderNameProfiles(t *testing.T) {
	for profile, opts := range [][]Options{nil, {Permissive(true)}, {Loose(true)}} {
		for _, name := range []string{"\u0132", "1x"} {
			for kind, write := range []func(*Encoder) error{
				func(e *Encoder) error { return e.BeginElement(Name{Local: name}) },
				func(e *Encoder) error {
					if err := e.BeginElement(Name{Local: "r"}); err != nil {
						return err
					}
					return e.Attr(Name{Local: name}, "v")
				},
				func(e *Encoder) error { return e.ProcInst(name, nil) },
			} {
				err := write(NewEncoder(io.Discard, opts...))
				if (err == nil) != (name == "\u0132") {
					t.Errorf("profile=%d kind=%d name=%q: err=%v", profile, kind, name, err)
				}
			}
		}
	}
}
