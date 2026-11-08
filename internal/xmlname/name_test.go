// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmlname

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

// slowValid is Valid without the ASCII table.
func slowValid(s string) bool {
	if len(s) == 0 {
		return false
	}
	c, n := utf8.DecodeRuneInString(s)
	if c == utf8.RuneError && n == 1 || !unicode.Is(first, c) {
		return false
	}
	for n < len(s) {
		s = s[n:]
		c, n = utf8.DecodeRuneInString(s)
		if c == utf8.RuneError && n == 1 || !unicode.Is(first, c) && !unicode.Is(second, c) {
			return false
		}
	}
	return true
}

func TestValidMatchesTables(t *testing.T) {
	check := func(s string) {
		t.Helper()
		want := slowValid(s)
		if got := ValidString(s); got != want {
			t.Fatalf("ValidString(%q) = %v, want %v", s, got, want)
		}
		if got := Valid([]byte(s)); got != want {
			t.Fatalf("Valid(%q) = %v, want %v", s, got, want)
		}
	}
	check("")
	for r := range rune(unicode.MaxRune + 1) {
		c := string(r)
		check(c)
		check("a" + c)
		check(c + "a")
	}
	for b := 0x80; b < 0x100; b++ {
		check(string([]byte{byte(b)}))
		check("a" + string([]byte{byte(b)}))
	}
	check("a\xe4\xb8")    // truncated
	check("\xef\xbf\xbd") // U+FFFD itself
}

func BenchmarkValidASCII(b *testing.B) {
	name := []byte("description")
	for b.Loop() {
		if !Valid(name) {
			b.Fatal("invalid")
		}
	}
}
