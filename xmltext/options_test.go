// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"strings"
	"testing"
)

func TestDecoderOptionsPrecedenceAndReset(t *testing.T) {
	const nested = `<a><b/></a>`
	d := NewDecoderBytes([]byte(nested), MaxDepth(1), MaxDepth(2), Options{})
	if _, err := readAll(t, d); err != nil {
		t.Fatalf("later depth limit: %v", err)
	}
	d.ResetBytes([]byte(nested), MaxDepth(2), MaxDepth(1))
	if _, err := readAll(t, d); err == nil || !strings.Contains(err.Error(), "max depth") {
		t.Fatalf("later restrictive depth limit: %v", err)
	}
	d.ResetBytes([]byte(nested), MaxDepth(1), MaxDepth(0))
	if _, err := readAll(t, d); err != nil {
		t.Fatalf("explicit default depth: %v", err)
	}
	d.ResetBytes([]byte(nested), MaxDepth(1))
	d.Reset(strings.NewReader(nested))
	if _, err := readAll(t, d); err != nil {
		t.Fatalf("reset must restore default depth: %v", err)
	}

	const fragments = `<a/><b/>`
	for _, opts := range [][]Options{
		{Permissive(true), AllowFragments(false)},
		{AllowFragments(false), Permissive(true)},
		{AllowFragments(true), AllowFragments(false)},
	} {
		d.ResetBytes([]byte(fragments), opts...)
		if _, err := readAll(t, d); err == nil {
			t.Fatal("explicit fragment rejection must override implied or earlier permission")
		}
	}
	d.ResetBytes([]byte(fragments), AllowFragments(false), AllowFragments(true))
	if _, err := readAll(t, d); err != nil {
		t.Fatalf("later fragment permission: %v", err)
	}
	d.ResetBytes([]byte(fragments))
	if _, err := readAll(t, d); err == nil {
		t.Fatal("reset must restore fragment rejection")
	}
}

func TestEncoderOptionsPrecedenceAndReset(t *testing.T) {
	var out bytes.Buffer
	e := NewEncoder(&out, WithIndent("", "\t"), WithIndent("", "  "), Options{})
	write := func(want string) {
		t.Helper()
		if err := e.BeginElement(Name{Local: "a"}); err != nil {
			t.Fatal(err)
		}
		if err := e.BeginElement(Name{Local: "b"}); err != nil {
			t.Fatal(err)
		}
		if err := e.EndElement(); err != nil {
			t.Fatal(err)
		}
		if err := e.EndElement(); err != nil {
			t.Fatal(err)
		}
		if err := e.Close(); err != nil {
			t.Fatal(err)
		}
		if got := out.String(); got != want {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
	write("<a>\n  <b/>\n</a>")
	out.Reset()
	e.Reset(&out, WithIndent("", "  "), WithIndent("", ""))
	write("<a><b/></a>")
	out.Reset()
	e.Reset(&out, WithIndent("", "  "))
	e.Reset(&out)
	write("<a><b/></a>")
}
