// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"
	"testing/iotest"

	stdxml "github.com/go-xml-experiment/xml/internal/legacyxml"
	"github.com/go-xml-experiment/xml/internal/xmlopts"
)

// The tests in this file compare xmltext with internal/legacyxml, a frozen
// copy of encoding/xml. They exist only outside the standard library.

// stdRawAttrs returns the lexical attribute names (prefix in Space) of each
// start element of in. std's Token() reports a declaration as {"xmlns", prefix}
// or {"", "xmlns"}, which is also what q:foo gets when q is bound to the URI
// "xmlns", and it loses the prefix of an attribute bound to the xmlns URI; only
// RawToken's lexical names tell these apart. strict, autoClose and entity
// configure the decoder as for Token().
func stdRawAttrs(in string, strict bool, autoClose []string, entity map[string]string) [][]stdxml.Name {
	d := stdxml.NewDecoder(strings.NewReader(in))
	d.Strict, d.AutoClose, d.Entity = strict, autoClose, entity
	var out [][]stdxml.Name
	for {
		tk, err := d.RawToken()
		if err != nil {
			return out
		}
		if v, ok := tk.(stdxml.StartElement); ok {
			names := make([]stdxml.Name, len(v.Attr))
			for i, a := range v.Attr {
				names[i] = a.Name
			}
			out = append(out, names)
		}
	}
}

// stdAttrName converts the name of one of std's attributes (raw is its lexical
// name) to xmltext's convention: a declaration is named by the xmlns namespace
// (xmlns="u" is {XMLNSNamespace, "xmlns"}), and a permissive xmlns:xmlns keeps the
// prefix as its Space. Intended difference: an attribute whose prefix is bound
// to the xmlns URI is reported like an unbound one, by its prefix, because only
// declarations have that Space.
func stdAttrName(n, raw stdxml.Name) Name {
	switch {
	case raw.Space != "xmlns" && !(raw.Space == "" && raw.Local == "xmlns"):
		if n.Space == XMLNSNamespace {
			return Name{raw.Space, n.Local}
		}
		return Name{n.Space, n.Local}
	case n.Space == "":
		return Name{XMLNSNamespace, "xmlns"}
	case n.Local == "xmlns":
		return Name{"xmlns", "xmlns"}
	}
	return Name{XMLNSNamespace, n.Local}
}

func stdTokens(t *testing.T, in string) ([]tok, error) {
	return legacyRun(in, strictCfg)
}

// legacyRun tokenizes in with the frozen v1 decoder.
func legacyRun(in string, c compatibilityCfg) ([]tok, error) {
	d := stdxml.NewDecoder(strings.NewReader(in))
	d.Strict = c.strict
	d.AutoClose = c.autoClose
	d.Entity = c.entity
	raws := stdRawAttrs(in, c.strict, c.autoClose, c.entity)
	var out []tok
	nStart := 0
	for {
		tk, err := d.Token()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		switch v := tk.(type) {
		case stdxml.StartElement:
			x := tok{kind: StartElement, name: Name{v.Name.Space, v.Name.Local}}
			var raw []stdxml.Name
			if nStart < len(raws) {
				raw = raws[nStart]
			}
			nStart++
			for i, a := range v.Attr {
				var rn stdxml.Name
				if i < len(raw) {
					rn = raw[i]
				}
				x.attrs = append(x.attrs, Attr{stdAttrName(a.Name, rn), a.Value})
			}
			out = append(out, x)
		case stdxml.EndElement:
			out = append(out, tok{kind: EndElement, name: Name{v.Name.Space, v.Name.Local}})
		case stdxml.CharData:
			out = append(out, tok{kind: Text, text: string(v)})
		case stdxml.Comment:
			out = append(out, tok{kind: Comment, text: string(v)})
		case stdxml.ProcInst:
			out = append(out, tok{kind: ProcInst, name: Name{Local: v.Target}, text: string(v.Inst)})
		case stdxml.Directive:
			out = append(out, tok{kind: Directive, text: string(v)})
		}
	}
}

// diffLegacy reports a description of the first difference between legacy
// and xmltext on in, or "" if they agree. Every legacy-accepted input
// must be accepted with equal tokens.
func diffLegacy(t testing.TB, in string, c compatibilityCfg) string {
	want, werr := legacyRun(in, c)
	got, gerr := readAll2(NewDecoderBytes([]byte(in), c.options()...))
	w, g := normalize(want), normalize(got)
	if werr != nil && gerr != nil {
		// Both reject. The token prefixes must agree, except that a Text
		// token cut short by the error may be missing on one side.
		n := min(len(w), len(g))
		if fmt.Sprint(w[:n]) != fmt.Sprint(g[:n]) {
			return fmt.Sprintf("prefix differs\n legacy %v (%v)\n xmltext %v (%v)", w, werr, g, gerr)
		}
		return ""
	}
	if (werr == nil) != (gerr == nil) || fmt.Sprint(w) != fmt.Sprint(g) {
		return fmt.Sprintf("legacy %v err=%v\nxmltext %v err=%v", w, werr, g, gerr)
	}
	return ""
}

func TestMatchesStd(t *testing.T) {
	for i, in := range corpus {
		want, err := stdTokens(t, in)
		if err != nil {
			t.Fatalf("corpus %d: std: %v", i, err)
		}
		want = normalize(want)
		// std emits EndElement for <a/> as well; xmltext does too.
		for name, mk := range readers(in) {
			got, err := readAll(t, mk())
			if err != nil {
				t.Errorf("corpus %d %s: %v", i, name, err)
				continue
			}
			got = normalize(got)
			if len(got) != len(want) {
				t.Errorf("corpus %d %s: %d tokens, want %d\n got %v\nwant %v", i, name, len(got), len(want), trunc(got), trunc(want))
				continue
			}
			for k := range got {
				if fmt.Sprint(got[k]) != fmt.Sprint(want[k]) && !(len(got[k].text) > 200 && got[k].text == want[k].text) {
					t.Errorf("corpus %d %s token %d:\n got %v\nwant %v", i, name, k, trunc(got[k:k+1]), trunc(want[k:k+1]))
					break
				}
			}
		}
	}
}

// In LegacyOutput mode the bytes equal encoding/xml's Encoder.EncodeToken
// output for the same token sequence, with and without indentation.
func TestEncoderLegacyMatchesStd(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for iter := 0; iter < 2000; iter++ {
		toks := randomTokens(rng, 0)
		var indent [2]string
		if rng.Intn(2) == 0 {
			indent = [2]string{[]string{"", ">"}[rng.Intn(2)], []string{" ", "\t", "  "}[rng.Intn(3)]}
		}
		var want bytes.Buffer
		se := stdxml.NewEncoder(&want)
		se.Indent(indent[0], indent[1])
		for _, tk := range toks {
			var err error
			switch tk.kind {
			case StartElement:
				st := stdxml.StartElement{Name: stdxml.Name{Space: tk.name.Space, Local: tk.name.Local}}
				for _, a := range tk.attrs {
					st.Attr = append(st.Attr, stdxml.Attr{Name: stdxml.Name{Space: a.Name.Space, Local: a.Name.Local}, Value: a.Value})
				}
				err = se.EncodeToken(st)
			case EndElement:
				err = se.EncodeToken(stdxml.EndElement{Name: stdxml.Name{Space: tk.name.Space, Local: tk.name.Local}})
			case Text:
				err = se.EncodeToken(stdxml.CharData(tk.text))
			case Comment:
				err = se.EncodeToken(stdxml.Comment(tk.text))
			case CData:
				continue // not a v1 token; EncodeToken has no CDATA
			}
			if err != nil {
				t.Fatalf("std: %v", err)
			}
		}
		se.Close()

		var got bytes.Buffer
		opts := []Options{xmlopts.LegacyOutput(true), BufferSize(1 + rng.Intn(50))}
		if indent != ([2]string{}) {
			opts = append(opts, WithIndent(indent[0], indent[1]))
		}
		e := NewEncoder(&got, opts...)
		for _, tk := range toks {
			if tk.kind == CData {
				continue
			}
			if err := playTokens(e, []genTok{tk}); err != nil {
				t.Fatalf("encoder: %v", err)
			}
		}
		e.Close()
		if got.String() != want.String() {
			t.Fatalf("iter %d indent %q\n got %q\nwant %q", iter, indent, got.String(), want.String())
		}
	}
}

// Escaping equals encoding/xml's EscapeText for text and attribute values.
func TestEscapeLegacyMatchesStd(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	alphabet := []byte("ab &<>\"'\t\n\r\x00\x01\x7f\xc3\xa9\xe2\x82\xac\xff\xf0\x9f\x98\x80\xef\xbf\xbe")
	e := NewEncoder(nil, xmlopts.LegacyOutput(true))
	for i := 0; i < 20000; i++ {
		n := rng.Intn(24)
		s := make([]byte, n)
		for j := range s {
			s[j] = alphabet[rng.Intn(len(alphabet))]
		}
		var want bytes.Buffer
		stdxml.EscapeText(&want, s)
		got, err := e.escapeInto(nil, s, escLegAttr, 0)
		if err != nil || string(got) != want.String() {
			t.Fatalf("%q: got %q (%v) want %q", s, got, err, want.String())
		}
	}
}

// TestStdSuiteInputsAgainstLegacy runs every such input through the frozen
// v1 decoder and xmltext under each v1-equivalent profile (Strict=true,
// Strict=false, Strict=false with HTML AutoClose/Entity) and requires equal
// acceptance and tokens (rejections need the same token prefix).
func TestStdSuiteInputsAgainstLegacy(t *testing.T) {
	inputs := stdSuiteInputs(t)
	t.Logf("%d distinct XML-looking constants from the encoding/xml tests", len(inputs))
	cfgs := map[string]compatibilityCfg{
		"strict": strictCfg,
		"loose":  looseCfg,
		"html":   {autoClose: htmlAuto, entity: htmlEntity},
	}
	bad, bothOK, bothReject := 0, 0, 0
	for name, c := range cfgs {
		for _, in := range inputs {
			_, werr := legacyRun(in, c)
			if _, gerr := readAll2(NewDecoderBytes([]byte(in), c.options()...)); werr == nil && gerr == nil {
				bothOK++
			} else if werr != nil && gerr != nil {
				bothReject++
			}
			if d := diffLegacy(t, in, c); d != "" {
				bad++
				t.Errorf("[%s] %q\n%s", name, in, d)
			}
		}
	}
	t.Logf("%d comparisons: %d accepted by both, %d rejected by both, %d differences", len(inputs)*len(cfgs), bothOK, bothReject, bad)
	if bothOK < 100 || bothReject < 50 {
		t.Errorf("suspiciously few accepted (%d) or rejected (%d) inputs", bothOK, bothReject)
	}
}

// FuzzAgainstStd checks chunked reads and compares accepted inputs with
// encoding/xml in both profiles, allowing only the documented differences.
func FuzzAgainstStd(f *testing.F) {
	for _, s := range corpus[:len(corpus)-1] {
		f.Add(s)
	}
	f.Add("<r>" + strings.Repeat("x", textWindow-2) + "&amp;한\r\n]]x</r>")
	for _, s := range []string{
		"<\u0132/>",
		`<?xml aversion="2.0"?><r/>`,
		`<?xml aencoding="latin1"?><r/>`,
		`<!DOCTYPE r [<!-- c --><!ELEMENT r ANY>]><r/>`,
		`<!DOCTYPE r "<!-- quoted -->"><r/>`,
		`<r><!-- c -->text</r>`,
		`<a xmlns:p="` + XMLNSNamespace + `" p:x="1"/>`,
		`<a xmlns:q="xmlns" q:foo="bar" xmlns:foo="x"/>`,
		"<r a=\"x\ny\tz\r\nw\rv&#9;&#10;&#13;&#x9;\" b='\t'/>",
		"<r xmlns:p=\"u\nv\"><p:c p:a=\"\r\n\"/></r>",
		"\ufeff<?xml version=\"1.0\" encoding=\"UTF-8\" standalone=\"yes\"?>\n<r>&#0000000065;&#x0000000041;</r>",
		"\ufeff <r/>",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, in string) {
		for _, permissive := range []bool{false, true} {
			got, err := readAll(t, NewDecoderBytes([]byte(in), Permissive(permissive)))
			got2, err2 := readAll(t, NewDecoder(iotest.OneByteReader(strings.NewReader(in)), Permissive(permissive)))
			// A streaming decoder can emit earlier Text pieces before discovering
			// an error later in the run; complete input validates the entire run.
			if (err == nil) != (err2 == nil) || err == nil && fmt.Sprint(normalize(got)) != fmt.Sprint(normalize(got2)) {
				t.Fatalf("permissive=%v chunked mismatch: %v vs %v\n%v\n%v", permissive, err, err2, got, got2)
			}
			if err != nil {
				continue
			}
			want, serr := stdTokens(t, in)
			if serr != nil {
				if !permissive {
					// Fifth-edition names include characters absent from std's
					// legacy tables. Do not excuse any other invalid-name error.
					if se, ok := serr.(*stdxml.SyntaxError); ok {
						if name, ok := strings.CutPrefix(se.Msg, "invalid XML name: "); ok && validNameString(name) {
							continue
						}
					}
				}
				t.Fatalf("permissive=%v std rejects accepted input: %v", permissive, serr)
			}
			if !permissive {
				want = strictStdView(t, in, want)
			}
			g, w := normalize(got), normalize(want)
			if !permissive {
				// The default profile preserves directive comments; std replaces
				// unquoted comments by one space. Compare every other token as-is.
				for i := range g {
					if g[i].kind == Directive {
						g[i].text = string(stripDirectiveComments(nil, []byte(g[i].text)))
					}
				}
			}
			if fmt.Sprint(g) != fmt.Sprint(w) {
				t.Fatalf("permissive=%v mismatch\n got %v\nwant %v", permissive, g, w)
			}
		}
	})
}

// strictStdView turns std's tokens for in into what the default profile reports
// where the two intentionally differ, and nothing else:
//   - attribute values: std keeps literal tab, newline and carriage return, the
//     default profile turns each into one space (a line end \r\n is one) and keeps
//     what a character reference produces (XML 1.0 section 3.3.3). The values are
//     taken from a second std parse of in with those characters (all
//     whitespace, so acceptance and structure do not change) replaced by spaces,
//     and so are the resolved element and attribute names;
//   - a UTF-8 byte order mark at the start of the stream is not text.
func strictStdView(t *testing.T, in string, want []tok) []tok {
	if strings.ContainsAny(in, "\t\n\r") {
		spaced := strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ").Replace(in)
		if alt, err := stdTokens(t, spaced); err == nil && len(alt) == len(want) {
			out := append([]tok(nil), want...)
			for i := range out {
				// names too: a namespace declaration's value is an attribute value
				if k := out[i].kind; (k == StartElement || k == EndElement) && alt[i].kind == k {
					out[i] = alt[i]
				}
			}
			want = out
		}
	}
	if strings.HasPrefix(in, "\ufeff") && len(want) > 0 && want[0].kind == Text && strings.HasPrefix(want[0].text, "\ufeff") {
		want = append([]tok(nil), want...)
		if want[0].text = strings.TrimPrefix(want[0].text, "\ufeff"); want[0].text == "" {
			want = want[1:]
		}
	}
	return want
}
