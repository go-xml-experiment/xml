// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"io"
	"strings"
)

// Helpers for fixed token, error, and position expectations.

type tokenCfg struct {
	name      string
	strict    bool
	autoClose []string
	entity    map[string]string
	raw       bool
}

func attrsOf(as []Attr) string {
	var sb strings.Builder
	for _, a := range as {
		fmt.Fprintf(&sb, "{%q %q %q}", a.Name.Space, a.Name.Local, a.Value)
	}
	return sb.String()
}

func renderNew(t Token) string {
	switch v := t.(type) {
	case StartElement:
		return fmt.Sprintf("S %q %q %q", v.Name.Space, v.Name.Local, attrsOf(v.Attr))
	case EndElement:
		return fmt.Sprintf("E %q %q", v.Name.Space, v.Name.Local)
	case CharData:
		return fmt.Sprintf("T %q", []byte(v))
	case Comment:
		return fmt.Sprintf("C %q", []byte(v))
	case ProcInst:
		return fmt.Sprintf("P %q %q", v.Target, v.Inst)
	case Directive:
		return fmt.Sprintf("D %q", []byte(v))
	}
	return fmt.Sprintf("? %T", t)
}

func errString(err error) string {
	if err == nil || err == io.EOF {
		return fmt.Sprint(err)
	}
	return fmt.Sprintf("%T: %v", err, err)
}

func runNew(in string, c tokenCfg) (out []string, err error) {
	d := NewDecoder(strings.NewReader(in))
	d.Strict, d.AutoClose, d.Entity = c.strict, c.autoClose, c.entity
	for len(out) < 5000 {
		var t Token
		if c.raw {
			t, err = d.RawToken()
		} else {
			t, err = d.Token()
		}
		if err != nil {
			return out, err
		}
		l, col := d.InputPos()
		out = append(out, fmt.Sprintf("%s @%d:%d:%d", renderNew(t), d.InputOffset(), l, col))
	}
	return out, nil
}

// compareTokens checks fixed token, position, and terminal-error expectations.
func compareTokens(in string, c tokenCfg) string {
	want := fixedValue("tokens", fmt.Sprintf("%#v\x00%s", c, in), 0)
	got, gerr := runNew(in, c)
	if fmt.Sprint(got) != want[0] || errString(gerr) != want[1] {
		return fmt.Sprintf("got %q (%s), want %s (%s)", got, errString(gerr), want[0], want[1])
	}
	return ""
}
