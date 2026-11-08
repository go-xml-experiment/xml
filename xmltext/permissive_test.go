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

// compatibilityCfg selects the legacy Decoder settings a permissive profile maps to.
type compatibilityCfg struct {
	strict    bool
	autoClose []string
	entity    map[string]string
}

func (c compatibilityCfg) options() []Options {
	o := []Options{Permissive(true)}
	if !c.strict {
		o = []Options{Loose(true)}
	}
	if c.autoClose != nil {
		o = append(o, AutoClose(c.autoClose))
	}
	if c.entity != nil {
		o = append(o, WithEntities(c.entity))
	}
	return o
}

// diffExpected compares tokens and acceptance with the fixed expectations.
func diffExpected(t testing.TB, in string, c compatibilityCfg) string {
	want, werr := fixedTokens(in, c)
	got, gerr := readAll2(NewDecoderBytes([]byte(in), c.options()...))
	w, g := normalize(want), normalize(got)
	if werr != nil && gerr != nil {
		// Both reject. The token prefixes must agree, except that a Text
		// token cut short by the error may be missing on one side.
		n := min(len(w), len(g))
		if fmt.Sprint(w[:n]) != fmt.Sprint(g[:n]) {
			return fmt.Sprintf("prefix differs\n expected %v (%v)\n xmltext %v (%v)", w, werr, g, gerr)
		}
		return ""
	}
	if (werr == nil) != (gerr == nil) || fmt.Sprint(w) != fmt.Sprint(g) {
		return fmt.Sprintf("expected %v err=%v\nxmltext %v err=%v", w, werr, g, gerr)
	}
	return ""
}

// readAll2 is readAll without the testing.T.
func readAll2(d *Decoder) ([]tok, error) { return readAll(nil, d) }

type ruleCase struct {
	rule string
	in   string
	cfg  compatibilityCfg
}

var strictCfg = compatibilityCfg{strict: true}
var looseCfg = compatibilityCfg{}

var htmlAuto = []string{"br", "hr", "img", "input", "meta", "p", "li"}

var htmlEntity = map[string]string{"nbsp": " ", "copy": "©", "eacute": "é"}

var ruleCases = []ruleCase{
	// permissive (legacy Strict = true)
	{"attr whitespace", `<a b="1"c="2"/>`, strictCfg},
	{"attr whitespace single quote", `<a b='1'c='2'd="3"/>`, strictCfg},
	{"duplicate attr", `<a x="1" x="2"/>`, strictCfg},
	{"unbound element prefix", `<p:a/>`, strictCfg},
	{"unbound attr prefix", `<a p:k="v"/>`, strictCfg},
	{"unbound end", `<p:a>t</p:a>`, strictCfg},
	{"unbound nested", `<a xmlns:q="u"><q:b p:c="1"><p:d/></q:b></a>`, strictCfg},
	{"xml prefix", `<a xml:lang="en"/>`, strictCfg},
	{"xmlns prefix element", `<xmlns:a/>`, strictCfg},
	{"xmlns attrs", `<a xmlns="u" xmlns:p="v" p:x="1"/>`, strictCfg},
	{"rebinding", `<a xmlns:p="u"><b xmlns:p="v"><p:c/></b><p:d/></a>`, strictCfg},
	{"empty default", `<a xmlns="u"><b xmlns=""><c/></b></a>`, strictCfg},
	{"multiple roots", `<a/><b/>`, strictCfg},
	{"text outside root", `hello<a/>tail`, strictCfg},
	{"cdata outside root", `<![CDATA[x]]><a/>`, strictCfg},
	{"no root", ``, strictCfg},
	{"text only", `just text`, strictCfg},
	{"comment only", `<!-- c -->`, strictCfg},
	{"bracket in text", `<a>]]></a>`, strictCfg},
	{"bracket after entity", `<a>]]&amp;></a>`, strictCfg},
	{"bracket in attr", `<a x="]]>"/>`, strictCfg},
	{"double dash comment", `<a><!-- a -- b --></a>`, strictCfg},
	{"triple dash comment", `<a><!-- a ---></a>`, strictCfg},
	{"empty comment", `<a><!----></a>`, strictCfg},
	{"comment bad char", "<a><!-- \x01 --></a>", strictCfg},
	{"comment bad utf8", "<a><!-- \xff --></a>", strictCfg},
	{"pi bad char", "<a><?t \x01?></a>", strictCfg},
	{"pi bad utf8", "<a><?t \xff?></a>", strictCfg},
	{"pi target no space", `<a><?t'x'?></a>`, strictCfg},
	{"pi target punct", `<a><?t=x?></a>`, strictCfg},
	{"pi no data", `<a><?t?></a>`, strictCfg},
	{"pi question data", `<a><?t ??></a>`, strictCfg},
	{"xml decl spaced", `<?xml version = "2.0"?><a/>`, strictCfg},
	{"xml decl version", `<?xml version="2.0"?><a/>`, strictCfg},
	{"xml decl utf8 mixed", `<?xml version='1.0' encoding='Utf-8'?><a/>`, strictCfg},
	{"directive bad char", "<!DOCTYPE a \x01><a/>", strictCfg},
	{"directive comment", `<!DOCTYPE a [<!-- c --><!ELEMENT a ANY>]><a/>`, strictCfg},
	{"directive comment dashes", `<!DOCTYPE a [<!-- c -- d ---->]><a/>`, strictCfg},
	{"directive comment gt", `<!DOCTYPE a [<!-- > --> ]><a/>`, strictCfg},
	{"directive comment quote", `<!DOCTYPE a [<!-- " --> ]><a/>`, strictCfg},
	{"directive quoted comment", `<!DOCTYPE a "<!-- x -->"><a/>`, strictCfg},
	{"directive nested", `<!DOCTYPE a [<!ENTITY x "y>z"> <!ELEMENT a (b|c)*>]><a/>`, strictCfg},
	{"directive odd start", `<! x><a/>`, strictCfg},
	{"directive empty", `<!><a/>`, strictCfg},
	{"directive dash start", `<!-x><a/>`, strictCfg},
	{"directive lone lt", `<!DOCTYPE a < b><a/>`, strictCfg},
	{"ref surrogate", `<a>&#xD800;</a>`, strictCfg},
	{"ref zeros", `<a>&#0000000000065;</a>`, strictCfg},
	{"ref huge", `<a>&#99999999999999999999;</a>`, strictCfg},
	{"ref out of range", `<a>&#x110000;</a>`, strictCfg},
	{"ref control", `<a>&#1;</a>`, strictCfg},
	{"ref nul", `<a>&#0;</a>`, strictCfg},
	{"ref fffe", `<a>&#xFFFE;</a>`, strictCfg},
	{"ref ok", `<a>&#x41;&#66;&lt;&gt;&amp;&apos;&quot;</a>`, strictCfg},
	{"ref unknown strict", `<a>&foo;</a>`, strictCfg},
	{"ref no semicolon strict", `<a>&amp</a>`, strictCfg},
	{"cr text", "<a>a\r\nb\rc</a>", strictCfg},
	{"cr attr", "<a x=\"a\r\nb\rc\"/>", strictCfg},
	{"end tag space", `<a></a  >`, strictCfg},
	{"end tag junk", `<a></a b>`, strictCfg},
	{"mismatch strict", `<a></b>`, strictCfg},
	{"mismatch prefix", `<a:x xmlns:a="u"></b:x>`, strictCfg},
	{"unexpected end", `</a>`, strictCfg},
	{"unterminated", `<a>`, strictCfg},
	{"unquoted strict", `<a b=c/>`, strictCfg},
	{"valueless strict", `<a b/>`, strictCfg},
	{"colon names", `<:a :b="1" c:="2"/>`, strictCfg},
	{"two colons", `<a:b:c/>`, strictCfg},
	{"bad name", `<1a/>`, strictCfg},
	{"slash space", `<a / >`, strictCfg},
	{"nested ok", `<a><b x="1">t</b><!--c--><?p d?></a>`, strictCfg},
	{"nonascii name", `<é à="1">ü</é>`, strictCfg},
	{"cdata cr", "<a><![CDATA[a\r\nb\rc & &amp; ]]></a>", strictCfg},
	{"bom", "\xef\xbb\xbf<a/>", strictCfg},

	// loose (legacy Strict = false)
	{"loose unknown entity", `<a>&foo; &amp &#xZZ; & &#65</a>`, looseCfg},
	{"loose entity amp", `<a>&a&amp;b</a>`, looseCfg},
	{"loose entity before lt", `<a>&foo<b/></a>`, looseCfg},
	{"loose entity attr", `<a x="&foo; &lt;"/>`, looseCfg},
	{"loose ref control", `<a>&#1;</a>`, looseCfg},
	{"loose valueless", `<a b c/>`, looseCfg},
	{"loose valueless prefixed", `<a p:b q/>`, looseCfg},
	{"loose valueless gt", `<a b>`, looseCfg},
	{"loose unquoted", `<a b=c d=e-f_g:h/>`, looseCfg},
	{"loose unquoted empty", `<a b=>x</a>`, looseCfg},
	{"loose unquoted stop", `<a b=c.d/>`, looseCfg},
	{"loose unquoted slash", `<a b=c/>`, looseCfg},
	{"loose unquoted mixed", `<a b=c d="e" f='g' h/>`, looseCfg},
	{"loose mismatch", `<a><b></a>`, looseCfg},
	{"loose mismatch deep", `<a><b><c></a>`, looseCfg},
	{"loose mismatch nomatch", `<a><b></x>`, looseCfg},
	{"loose mismatch case", `<a><B></b></a>`, looseCfg},
	{"loose end unexpected", `</a>`, looseCfg},
	{"loose end unexpected after", `<a/></a>`, looseCfg},
	{"loose mismatch prefix", `<a:x xmlns:a="u"></b:x>`, looseCfg},
	{"loose mismatch prefix2", `<a:x xmlns:a="u" xmlns:b="v"></b:x>`, looseCfg},
	{"loose mismatch ns", `<r xmlns:a="u"><a:x><a:y></a:x></r>`, looseCfg},
	{"loose eof open", `<a><b>`, looseCfg},
	{"loose html", `<html><body><p>one<p>two<br>x<br/>y<img src=a.png alt="b"></body></html>`, compatibilityCfg{autoClose: htmlAuto}},
	{"loose html entities", `<p>&nbsp;&copy;&eacute;&unknown;</p>`, compatibilityCfg{autoClose: htmlAuto, entity: htmlEntity}},
	{"autoclose end tag", `<a><br></br><br></BR>x</a>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose case end", `<a><BR></br>x</a>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose text", `<br>text`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose comment", `<a><br><!--c--><?p d?></a>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose eof", `<a><br>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose nested", `<p><p><p>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose bad next", "<a><br>\x01</a>", compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose bad end", `<a><br></a x></a>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose prefix", `<a><x:br xmlns:x="u"><b/></a>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose selfclose", `<a><br/><br/></a>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose end of parent", `<a><li>x</a>`, compatibilityCfg{autoClose: htmlAuto}},
	{"autoclose start in start", `<li><li x="1">a</li></li>`, compatibilityCfg{autoClose: htmlAuto}},
	{"loose comment double dash", `<a><!-- a -- b --></a>`, looseCfg},
	{"loose bracket", `<a>]]></a>`, looseCfg},
	{"loose multiple roots", `<a/>x<b/>`, looseCfg},
	{"loose dup attr", `<a x=1 x=2/>`, looseCfg},
}

func TestLegacyHTMLSettings(t *testing.T) {
	c := compatibilityCfg{autoClose: []string{"p", "br", "img"}, entity: map[string]string{"nbsp": "\u00a0", "copy": "©", "Alpha": "Α"}}
	in := `<html><body><p>&nbsp;&copy;&Alpha;</p><BR>tail<img src="a.png"></body></html>`
	if diff := diffExpected(t, in, c); diff != "" {
		t.Fatal(diff)
	}
	for _, in := range []string{`<a>&#` + strings.Repeat("0", 100) + `65;</a>`, `<a>&#4294967296;</a>`, `<a><A xmlns></a>`, `&`, `&amp`} {
		for _, cfg := range []compatibilityCfg{strictCfg, looseCfg} {
			if diff := diffExpected(t, in, cfg); diff != "" {
				t.Fatalf("%q: %s", in, diff)
			}
		}
	}
}

func TestPermissiveRules(t *testing.T) {
	for _, c := range ruleCases {
		if d := diffExpected(t, c.in, c.cfg); d != "" {
			t.Errorf("%s: %q\n%s", c.rule, c.in, d)
		}
	}
}

// Every rule must also hold for chunked reads.
func TestPermissiveRulesChunked(t *testing.T) {
	for _, c := range ruleCases {
		a, aerr := readAll2(NewDecoderBytes([]byte(c.in), c.cfg.options()...))
		for _, mk := range []func() io.Reader{
			func() io.Reader { return iotest.OneByteReader(strings.NewReader(c.in)) },
			func() io.Reader { return iotest.HalfReader(strings.NewReader(c.in)) },
		} {
			b, berr := readAll2(NewDecoder(mk(), c.cfg.options()...))
			if (aerr == nil) != (berr == nil) || fmt.Sprint(normalize(a)) != fmt.Sprint(normalize(b)) {
				t.Errorf("%s: %q: chunked differs\n%v %v\n%v %v", c.rule, c.in, a, aerr, b, berr)
			}
		}
	}
}

func TestStrictDefaultUnchanged(t *testing.T) {
	// Without options the strict rules still apply.
	for _, in := range []string{
		`<a b="1"c="2"/>`, `<a x="1" x="2"/>`, `<p:a/>`, `<a/><b/>`, `x<a/>`, `<a>]]></a>`, `<a b/>`, `<a b=c/>`,
		`<a><b></a>`, `<a>&foo;</a>`, "<a><!-- \x01 --></a>", `<a><?t'x'?></a>`, `<!><a/>`,
	} {
		if _, err := readAll2(NewDecoderBytes([]byte(in))); err == nil {
			t.Errorf("default accepted %q", in)
		}
	}
}

func FuzzPermissive(f *testing.F) {
	for _, c := range ruleCases {
		f.Add(c.in, byte(c.mode()))
	}
	for _, s := range corpus[:len(corpus)-1] {
		f.Add(s, byte(0))
		f.Add(s, byte(1))
	}
	f.Fuzz(func(t *testing.T, in string, mode byte) {
		c := []compatibilityCfg{strictCfg, looseCfg, {autoClose: htmlAuto, entity: htmlEntity}}[int(mode)%3]
		a, ae := readAll2(NewDecoderBytes([]byte(in), c.options()...))
		b, be := readAll2(NewDecoder(iotest.OneByteReader(strings.NewReader(in)), c.options()...))
		if (ae == nil) != (be == nil) || ae == nil && fmt.Sprint(normalize(a)) != fmt.Sprint(normalize(b)) {
			t.Fatalf("chunked mismatch: %v / %v\n%v\n%v", ae, be, a, b)
		}
	})
}

func (c ruleCase) mode() int {
	switch {
	case c.cfg.strict:
		return 0
	case c.cfg.autoClose != nil:
		return 2
	}
	return 1
}
