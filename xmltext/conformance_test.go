// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
)

// confRead reads every token of in with the default profile (plus opts) from
// bytes and from a reader that returns one byte at a time; both must agree.
func confRead(t *testing.T, in string, opts ...Options) ([]tok, error) {
	t.Helper()
	got, err := readAll(t, NewDecoderBytes([]byte(in), opts...))
	got2, err2 := readAll(t, NewDecoder(iotest.OneByteReader(strings.NewReader(in)), opts...))
	if (err == nil) != (err2 == nil) || err != nil && err.Error() != err2.Error() ||
		err == nil && !reflect.DeepEqual(normalize(got), normalize(got2)) {
		t.Fatalf("input %q: bytes and one-byte reader disagree\n bytes  %v %v\n reader %v %v", in, got, err, got2, err2)
	}
	return got, err
}

// confAttr is the value of the attribute a of the first start element.
func confAttr(toks []tok) (string, bool) {
	for _, tk := range toks {
		if tk.kind == StartElement {
			for _, a := range tk.attrs {
				if a.Name.Local == "a" && a.Name.Space == "" {
					return a.Value, true
				}
			}
			return "", false
		}
	}
	return "", false
}

// confText is the concatenated character data of toks.
func confText(toks []tok) string {
	var b strings.Builder
	for _, tk := range toks {
		if tk.kind == Text || tk.kind == CData {
			b.WriteString(tk.text)
		}
	}
	return b.String()
}

// confCase is one input of TestStrictProfileConformance. err is a substring of
// the error the default profile reports, or "" if it must accept the input.
type confCase struct {
	name, in string
	err      string
	attr     string // wanted value of attribute a, if hasAttr
	hasAttr  bool
	text     string // wanted character data, if hasText
	hasText  bool
	space    string // wanted namespace of the first child element, if hasSpace
	hasSpace bool
}

func confAttrCase(name, in, want string) confCase {
	return confCase{name: name, in: in, attr: want, hasAttr: true}
}

func confTextCase(name, in, want string) confCase {
	return confCase{name: name, in: in, text: want, hasText: true}
}

func confSpaceCase(name, in, want string) confCase {
	return confCase{name: name, in: in, space: want, hasSpace: true}
}

func confErrCase(name, in, err string) confCase { return confCase{name: name, in: in, err: err} }

func confOKCase(name, in string) confCase { return confCase{name: name, in: in} }

var confCases = func() []confCase {
	var cs []confCase
	add := func(c ...confCase) { cs = append(cs, c...) }

	// review #7: the XML declaration is only the first thing in the document,
	// and the target xml in any letter case is reserved
	const (
		misplaced = "only allowed at the start"
		reserved  = "is reserved"
		colon     = "contains a colon"
	)
	add(
		confOKCase("7 declaration first", `<?xml version="1.0"?><r/>`),
		confOKCase("7 declaration then newline", "<?xml version=\"1.0\"?>\n<r/>"),
		confErrCase("7 declaration after root", `<r/><?xml version="1.0"?>`, misplaced),
		confErrCase("7 declaration inside root", `<r><?xml version="1.0"?></r>`, misplaced),
		confErrCase("7 declaration after comment", `<!--c--><?xml version="1.0"?><r/>`, misplaced),
		confErrCase("7 declaration after space", ` <?xml version="1.0"?><r/>`, misplaced),
		confErrCase("7 declaration after newline", "\n<?xml version=\"1.0\"?><r/>", misplaced),
		confErrCase("7 second declaration", `<?xml version="1.0"?><?xml version="1.0"?><r/>`, misplaced),
		confErrCase("7 lowercase xml data", `<r/><?xml x?>`, misplaced),
		confErrCase("7 XmL", `<?XmL x?><r/>`, reserved),
		confErrCase("7 XML", `<?XML version="1.0"?><r/>`, reserved),
		confErrCase("7 Xml inside", `<r><?Xml x?></r>`, reserved),
		confErrCase("7 xML inside", `<r><?xML?></r>`, reserved),
		confOKCase("7 xml-stylesheet", `<?xml-stylesheet href="a.css"?><r/>`),
		confOKCase("7 xml-stylesheet inside", `<r><?xml-stylesheet href="a.css"?></r>`),
		confOKCase("7 xmlfoo", `<?xmlfoo x?><r/>`),
		confOKCase("7 xm", `<?xm x?><r/>`),
		confOKCase("7 xmll", `<r><?xmll x?></r>`),
		confOKCase("7 declaration then stylesheet", `<?xml version="1.0"?><?xml-stylesheet href="a"?><r/>`),
		confOKCase("7 declaration after BOM", "\ufeff<?xml version=\"1.0\"?><r/>"),
		confOKCase("7 BOM then root", "\ufeff<r/>"),
		confOKCase("7 BOM then space and root", "\ufeff <r/>"),
		confOKCase("7 BOM, declaration, stylesheet", "\ufeff<?xml version=\"1.0\"?><?xml-stylesheet href=\"a\"?><r/>"),
		confErrCase("7 declaration after BOM and space", "\ufeff <?xml version=\"1.0\"?><r/>", misplaced),
		confErrCase("7 second declaration after BOM", "\ufeff<?xml version=\"1.0\"?><?xml version=\"1.0\"?><r/>", misplaced),
		confErrCase("7 XmL after BOM", "\ufeff<?XmL x?><r/>", reserved),
		confErrCase("7 two BOMs", "\ufeff\ufeff<?xml version=\"1.0\"?><r/>", "text outside of root"),
		confErrCase("7 BOM after root", "<r/>\ufeff", "text outside of root"),
		confErrCase("7 truncated BOM", "\xef\xbb<r/>", "invalid UTF-8"),
		confErrCase("7 colon target", `<?a:b x?><r/>`, colon),
		confErrCase("7 colon target inside", `<r><?p:q?></r>`, colon),
		confErrCase("7 colon xml target", `<?xml:b x?><r/>`, colon),
		confErrCase("7 leading colon target", `<r><?:b?></r>`, colon),
	)

	// review #11d: XMLDecl grammar
	const (
		beginVersion = "must begin with version"
		unsupported  = "unsupported version: "
	)
	add(
		confOKCase("11d version", `<?xml version="1.0"?><r/>`),
		confOKCase("11d single quotes", `<?xml version='1.0'?><r/>`),
		confOKCase("11d spaces around =", `<?xml version = "1.0" ?><r/>`),
		confOKCase("11d newline separators", "<?xml\nversion=\"1.0\"\tencoding=\"UTF-8\"\r\n?><r/>"),
		confOKCase("11d encoding", `<?xml version="1.0" encoding="UTF-8"?><r/>`),
		confOKCase("11d encoding single quotes", `<?xml version="1.0" encoding='utf-8'?><r/>`),
		confOKCase("11d standalone yes", `<?xml version="1.0" standalone="yes"?><r/>`),
		confOKCase("11d standalone no", `<?xml version="1.0" standalone='no'?><r/>`),
		confOKCase("11d all three", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><r/>`),
		confErrCase("11d no version", `<?xml encoding="UTF-8"?><r/>`, beginVersion),
		confErrCase("11d empty declaration", `<?xml?><r/>`, beginVersion),
		confErrCase("11d blank declaration", `<?xml  ?><r/>`, beginVersion),
		confErrCase("11d standalone only", `<?xml standalone="yes"?><r/>`, beginVersion),
		confErrCase("11d prefixed version", `<?xml aversion="1.0"?><r/>`, beginVersion),
		confErrCase("11d standalone maybe", `<?xml version="1.0" standalone="maybe"?><r/>`, "yes or no"),
		confErrCase("11d standalone YES", `<?xml version="1.0" standalone="YES"?><r/>`, "yes or no"),
		confErrCase("11d standalone empty", `<?xml version="1.0" standalone=""?><r/>`, "yes or no"),
		confErrCase("11d wrong order", `<?xml version="1.0" standalone="yes" encoding="UTF-8"?><r/>`, "unexpected text"),
		confErrCase("11d encoding first", `<?xml encoding="UTF-8" version="1.0"?><r/>`, beginVersion),
		confErrCase("11d other pseudo-attribute", `<?xml version="1.0" foo="x"?><r/>`, "unexpected text"),
		confErrCase("11d trailing junk", `<?xml version="1.0" x?><r/>`, "unexpected text"),
		confErrCase("11d duplicate version", `<?xml version="1.0" version="1.0"?><r/>`, "unexpected text"),
		confErrCase("11d duplicate encoding", `<?xml version="1.0" encoding="UTF-8" encoding="UTF-8"?><r/>`, "unexpected text"),
		confErrCase("11d duplicate standalone", `<?xml version="1.0" standalone="no" standalone="no"?><r/>`, "unexpected text"),
		confErrCase("11d no space before encoding", `<?xml version="1.0"encoding="UTF-8"?><r/>`, "expected whitespace"),
		confErrCase("11d no space before standalone", `<?xml version="1.0" encoding="UTF-8"standalone="no"?><r/>`, "expected whitespace"),
		confErrCase("11d encoding starts with digit", `<?xml version="1.0" encoding="1abc"?><r/>`, "invalid encoding name"),
		confErrCase("11d encoding empty", `<?xml version="1.0" encoding=""?><r/>`, "invalid encoding name"),
		confErrCase("11d encoding space", `<?xml version="1.0" encoding="a b"?><r/>`, "invalid encoding name"),
		confErrCase("11d encoding slash", `<?xml version="1.0" encoding="a/b"?><r/>`, "invalid encoding name"),
		confErrCase("11d encoding leading dash", `<?xml version="1.0" encoding="-a"?><r/>`, "invalid encoding name"),
		confErrCase("11d unquoted version", `<?xml version=1.0?><r/>`, "unquoted"),
		confErrCase("11d mismatched quotes", `<?xml version="1.0'?><r/>`, "unterminated"),
		confErrCase("11d missing =", `<?xml version "1.0"?><r/>`, "expected ="),
		confErrCase("11d version 1.1", `<?xml version="1.1"?><r/>`, unsupported+"1.1"),
		confErrCase("11d version 2.0", `<?xml version="2.0"?><r/>`, unsupported+"2.0"),
		confErrCase("11d version empty", `<?xml version=""?><r/>`, "unsupported version"),
		confErrCase("11d version 1.1 wins", `<?xml version="1.1" standalone="maybe"?><r/>`, unsupported+"1.1"),
	)

	// review #11a: attribute value normalization (no DTD: every attribute is CDATA)
	add(
		confAttrCase("11a plain", `<r a="x y"/>`, "x y"),
		confAttrCase("11a newline tab", "<r a=\"x\ny\tz\"/>", "x y z"),
		confAttrCase("11a carriage return", "<r a=\"x\ry\"/>", "x y"),
		confAttrCase("11a CRLF is one space", "<r a=\"x\r\ny\"/>", "x y"),
		confAttrCase("11a two CRLF", "<r a=\"a\r\n\r\nb\"/>", "a  b"),
		confAttrCase("11a LF CR", "<r a=\"a\n\rb\"/>", "a  b"),
		confAttrCase("11a CR CR LF", "<r a=\"a\r\r\nb\"/>", "a  b"),
		confAttrCase("11a only whitespace", "<r a=\"\t\n\r\"/>", "   "),
		confAttrCase("11a leading and trailing", "<r a=\"\tx\n\"/>", " x "),
		confAttrCase("11a single quotes", "<r a='x\ty'/>", "x y"),
		confAttrCase("11a non-ASCII", "<r a=\"한\n글\t😀\r\n!\"/>", "한 글 😀 !"),
		confAttrCase("11a refs stay: &#9;", `<r a="x&#9;y"/>`, "x\ty"),
		confAttrCase("11a refs stay: &#10;", `<r a="x&#10;y"/>`, "x\ny"),
		confAttrCase("11a refs stay: &#13;", `<r a="x&#13;y"/>`, "x\ry"),
		confAttrCase("11a refs stay: &#x9;", `<r a="x&#x9;y"/>`, "x\ty"),
		confAttrCase("11a refs stay: &#xA;", `<r a="x&#xA;y"/>`, "x\ny"),
		confAttrCase("11a refs stay: &#xd;", `<r a="x&#xd;y"/>`, "x\ry"),
		confAttrCase("11a refs stay: &#32;", `<r a="x&#32;y"/>`, "x y"),
		confAttrCase("11a literal and ref", "<r a=\"a\t&#9;\n&#10;\r\n&#13;\"/>", "a \t \n \r"),
		confAttrCase("11a ref next to CRLF", "<r a=\"&#13;\r\n&amp;\"/>", "\r &"),
		confAttrCase("11a entity then whitespace", "<r a=\"&lt;\n&gt;\"/>", "< >"),
		confAttrCase("11a no whitespace", `<r a="plain"/>`, "plain"),
		confAttrCase("11a entity only", `<r a="a&amp;b"/>`, "a&b"),
		confAttrCase("11a many attributes", "<r b=\"1\n2\" a=\"x\ty\" c=\"&#10;\"/>", "x y"),
		confTextCase("11a text is not normalized", "<r a=\"x\">p\tq\nr</r>", "p\tq\nr"),
		confTextCase("11a text CRLF is one newline", "<r a=\"x\">p\r\nq\rr</r>", "p\nq\nr"),
		confSpaceCase("11a xmlns prefix", "<r xmlns:p=\"urn:a\nb\"><p:c/></r>", "urn:a b"),
		confSpaceCase("11a xmlns prefix CRLF", "<r xmlns:p=\"urn:a\r\nb\tc\"><p:c/></r>", "urn:a b c"),
		confSpaceCase("11a xmlns default", "<r xmlns=\"urn:a\tb\"><c/></r>", "urn:a b"),
		confSpaceCase("11a xmlns ref stays", "<r xmlns:p=\"urn:a&#10;b\"><p:c/></r>", "urn:a\nb"),
	)

	// review #11b: leading zeros in character references
	add(
		confTextCase("11b decimal", `<r>&#0000000065;</r>`, "A"),
		confTextCase("11b hex", `<r>&#x0000000041;</r>`, "A"),
		confTextCase("11b many zeros", `<r>&#`+strings.Repeat("0", 100)+`65;</r>`, "A"),
		confTextCase("11b many hex zeros", `<r>&#x`+strings.Repeat("0", 100)+`41;</r>`, "A"),
		confTextCase("11b exactly eight digits", `<r>&#00000065;</r>`, "A"),
		confTextCase("11b max code point", `<r>&#x0000010FFFF;</r>`, "\U0010FFFF"),
		confTextCase("11b max code point decimal", `<r>&#0001114111;</r>`, "\U0010FFFF"),
		confTextCase("11b tab", `<r>&#0000000009;</r>`, "\t"),
		confAttrCase("11b attribute decimal", `<r a="&#0000000065;"/>`, "A"),
		confAttrCase("11b attribute hex", `<r a="&#x0000000041;&#x000000042;"/>`, "AB"),
		confAttrCase("11b attribute newline stays", `<r a="&#0000000010;"/>`, "\n"),
		confErrCase("11b zero", `<r>&#0000000000;</r>`, "invalid character entity"),
		confErrCase("11b zero one digit", `<r>&#0;</r>`, "invalid character entity"),
		confErrCase("11b hex zero", `<r>&#x000000000000;</r>`, "invalid character entity"),
		confErrCase("11b control", `<r>&#0000000001;</r>`, "invalid character entity"),
		confErrCase("11b above max", `<r>&#0001114112;</r>`, "invalid character entity"),
		confErrCase("11b above max hex", `<r>&#x0000110000;</r>`, "invalid character entity"),
		confErrCase("11b surrogate", `<r>&#x00000D800;</r>`, "invalid character entity"),
		confErrCase("11b FFFE", `<r>&#x000000FFFE;</r>`, "invalid character entity"),
		confErrCase("11b nine significant digits", `<r>&#000100000000;</r>`, "invalid character entity"),
		confErrCase("11b overflow", `<r>&#0004294967361;</r>`, "invalid character entity"),
		confErrCase("11b bad digit after zeros", `<r>&#00000x41;</r>`, "invalid character entity"),
		confErrCase("11b hex digit in decimal", `<r>&#000000A1;</r>`, "invalid character entity"),
		confErrCase("11b only prefix", `<r>&#;</r>`, "invalid character entity"),
		confErrCase("11b only x", `<r>&#x;</r>`, "invalid character entity"),
		confErrCase("11b attribute zero", `<r a="&#000000;"/>`, "invalid character entity"),
		confErrCase("11b attribute above max", `<r a="&#x0000110000;"/>`, "invalid character entity"),
	)
	return cs
}()

// TestStrictProfileConformance fixes the default profile's reading of the inputs
// in review items 7 and 11 (XML declaration placement and grammar, PI targets,
// attribute value normalization, character reference digits).
func TestStrictProfileConformance(t *testing.T) {
	for _, c := range confCases {
		t.Run(c.name, func(t *testing.T) {
			toks, err := confRead(t, c.in)
			switch {
			case c.err != "" && err == nil:
				t.Fatalf("accepted %q, want error containing %q", c.in, c.err)
			case c.err != "":
				if !strings.Contains(err.Error(), c.err) {
					t.Fatalf("input %q: error %q does not contain %q", c.in, err, c.err)
				}
				if _, ok := err.(*SyntaxError); !ok {
					t.Fatalf("input %q: %T is not a *SyntaxError", c.in, err)
				}
			case err != nil:
				t.Fatalf("rejected %q: %v", c.in, err)
			}
			if c.hasAttr {
				if v, ok := confAttr(toks); !ok || v != c.attr {
					t.Fatalf("input %q: attribute a = %q (found %v), want %q", c.in, v, ok, c.attr)
				}
			}
			if c.hasText {
				if v := confText(toks); v != c.text {
					t.Fatalf("input %q: text %q, want %q", c.in, v, c.text)
				}
			}
			if c.hasSpace {
				var got []string
				for _, tk := range toks {
					if tk.kind == StartElement && tk.name.Local == "c" {
						got = append(got, tk.name.Space)
					}
				}
				if len(got) != 1 || got[0] != c.space {
					t.Fatalf("input %q: namespace of c is %q, want %q", c.in, got, c.space)
				}
			}

			// encoding/xml and the permissive profile, where std accepts the input,
			// must agree; they keep whatever the default profile normalizes or rejects.
			want, serr := expectedTokens(t, c.in)
			if serr != nil {
				return
			}
			got, err := confRead(t, c.in, Permissive(true))
			if err != nil {
				t.Fatalf("encoding/xml accepts %q, Permissive rejects it: %v", c.in, err)
			}
			if !reflect.DeepEqual(normalize(got), normalize(want)) {
				t.Fatalf("input %q: Permissive differs from encoding/xml\n got %v\nwant %v", c.in, normalize(got), normalize(want))
			}
		})
	}
}

// The permissive profile keeps encoding/xml's reading of the cases the default
// profile rejects (and std's value of an attribute is not normalized).
func TestPermissiveKeepsStdReading(t *testing.T) {
	for _, in := range []string{
		`<r/><?xml version="1.0"?>`,
		`<?XmL x?><r/>`,
		`<?a:b x?><r/>`,
		`<?xml encoding="UTF-8"?><r/>`,
		`<?xml version="1.0" standalone="maybe"?><r/>`,
		`<?xml version="1.0" encoding=""?><r/>`,
		"<r a=\"x\ny\tz\r\nw\"/>",
		`<r>&#0000000065;</r>`,
	} {
		want, serr := expectedTokens(t, in)
		if serr != nil {
			t.Fatalf("encoding/xml rejects %q: %v", in, serr)
		}
		got, err := confRead(t, in, Permissive(true))
		if err != nil || !reflect.DeepEqual(normalize(got), normalize(want)) {
			t.Errorf("input %q: Permissive %v %v, encoding/xml %v", in, got, err, want)
		}
	}
	got, err := confRead(t, "<r a=\"x\ny\tz\r\nw\"/>", Permissive(true))
	if v, _ := confAttr(got); err != nil || v != "x\ny\tz\nw" {
		t.Fatalf("Permissive attribute value %q, %v", v, err)
	}
}

// A declaration after the start never switches the character set in the default
// profile; the one at the start does, and Permissive follows encoding/xml.
func TestStrictCharsetSwitchOnlyAtStart(t *testing.T) {
	for _, c := range []struct {
		name, in   string
		perm       bool
		wantErr    bool
		wantLabels []string
	}{
		{"start", `<?xml version="1.0" encoding="x-A"?><r/>`, false, false, []string{"x-A"}},
		{"after root", `<r/><?xml version="1.0" encoding="x-A"?>`, false, true, nil},
		{"inside root", `<r><?xml version="1.0" encoding="x-A"?></r>`, false, true, nil},
		{"after space", ` <?xml version="1.0" encoding="x-A"?><r/>`, false, true, nil},
		{"second", `<?xml version="1.0" encoding="x-A"?><?xml version="1.0" encoding="x-B"?><r/>`, false, true, []string{"x-A"}},
		{"permissive inside root", `<r><?xml version="1.0" encoding="x-A"?></r>`, true, false, []string{"x-A"}},
		{"permissive second", `<?xml version="1.0" encoding="x-A"?><r><?xml encoding="x-B"?></r>`, true, false, []string{"x-A", "x-B"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			for _, reader := range []bool{false, true} {
				var labels []string
				opts := []Options{Permissive(c.perm), WithCharsetReader(func(label string, r io.Reader) (io.Reader, error) {
					labels = append(labels, label)
					return r, nil
				})}
				var d *Decoder
				if reader {
					d = NewDecoder(iotest.OneByteReader(strings.NewReader(c.in)), opts...)
				} else {
					d = NewDecoderBytes([]byte(c.in), opts...)
				}
				_, err := readAll(t, d)
				if (err != nil) != c.wantErr || !reflect.DeepEqual(labels, c.wantLabels) {
					t.Fatalf("reader=%v: error %v, charset labels %v; want error %v, labels %v", reader, err, labels, c.wantErr, c.wantLabels)
				}
			}
		})
	}
}

// Version and the unsupported-encoding messages are matched by the v1 layer.
func TestStrictDeclarationErrorPrefixes(t *testing.T) {
	for _, c := range []struct{ in, prefix string }{
		{`<?xml version="1.1"?><r/>`, "unsupported version: "},
		{`<?xml version="1.0" encoding="x-A"?><r/>`, "unsupported encoding: "},
	} {
		_, err := confRead(t, c.in)
		se, ok := err.(*SyntaxError)
		if !ok || !strings.HasPrefix(se.Msg, c.prefix) {
			t.Errorf("input %q: %v", c.in, err)
		}
	}
}

// Entity names registered with WithEntities are not limited to 64 bytes.
func TestLongEntityNames(t *testing.T) {
	for _, n := range []int{1, 63, 64, 65, 66, 70, 200, 5000} {
		name := strings.Repeat("e", n)
		ents := WithEntities(map[string]string{name: "<v>"})
		for _, c := range []struct{ in, wantText, wantAttr string }{
			{"<r>&" + name + ";</r>", "<v>", ""},
			{"<r>a&" + name + ";b&amp;&" + name + ";</r>", "a<v>b&<v>", ""},
			{"<r a=\"&" + name + ";\n&" + name + ";\"/>", "", "<v> <v>"},
			{"<r a=\"x&" + name + ";\"><![CDATA[&" + name + ";]]></r>", "&" + name + ";", "x<v>"},
		} {
			toks, err := confRead(t, c.in, ents)
			if err != nil {
				t.Fatalf("name length %d, input %q: %v", n, c.in, err)
			}
			if v := confText(toks); v != c.wantText {
				t.Fatalf("name length %d, input %q: text %q, want %q", n, c.in, v, c.wantText)
			}
			if v, _ := confAttr(toks); c.wantAttr != "" && v != c.wantAttr {
				t.Fatalf("name length %d, input %q: attribute %q, want %q", n, c.in, v, c.wantAttr)
			}
		}
		// the name must still be registered, and the longest one sets the bound
		for _, in := range []string{"<r>&" + name + "x;</r>", "<r>&" + name + "</r>", "<r a=\"&" + name + "x;\"/>"} {
			if _, err := confRead(t, in, ents); err == nil || !strings.Contains(err.Error(), "invalid character entity") {
				t.Fatalf("name length %d, input %q: %v", n, in, err)
			}
		}
	}
	// without registration, 65+ byte names are as unknown as short ones
	for _, in := range []string{"<r>&" + strings.Repeat("e", 70) + ";</r>", "<r a=\"&" + strings.Repeat("e", 70) + ";\"/>"} {
		if _, err := confRead(t, in); err == nil || !strings.Contains(err.Error(), "invalid character entity") {
			t.Fatalf("input %q: %v", in, err)
		}
	}
	// AllowUnknownEntities keeps a long unknown reference as text
	toks, err := confRead(t, "<r>&"+strings.Repeat("e", 70)+";</r>", AllowUnknownEntities(true))
	if err != nil || confText(toks) != "&"+strings.Repeat("e", 70)+";" {
		t.Fatalf("unknown long reference: %q %v", confText(toks), err)
	}
}

// A long entity reference that straddles the end of a SplitText window is
// delivered whole: the piece before it ends at the '&'.
func TestLongEntityAcrossWindow(t *testing.T) {
	for _, n := range []int{64, 65, 100, 1000} {
		name := strings.Repeat("n", n)
		value := "[" + strings.Repeat("v", 10) + "]"
		ents := WithEntities(map[string]string{name: value})
		ref := "&" + name + ";"
		for shift := -3; shift <= len(ref)+3; shift++ {
			pad := strings.Repeat("x", textWindow+shift)
			in := "<r>" + pad + ref + "tail" + ref + "</r>"
			want := pad + value + "tail" + value
			readers := map[string]func() *Decoder{
				"bytes":  func() *Decoder { return NewDecoderBytes([]byte(in), ents, SplitText(true)) },
				"reader": func() *Decoder { return NewDecoder(strings.NewReader(in), ents, SplitText(true)) },
				"buffer": func() *Decoder { return NewDecoder(bytes.NewBufferString(in), ents, SplitText(true)) },
				"half":   func() *Decoder { return NewDecoder(iotest.HalfReader(strings.NewReader(in)), ents, SplitText(true)) },
			}
			for name, mk := range readers {
				var text strings.Builder
				d := mk()
				for {
					tk, err := d.ReadToken()
					if err == io.EOF {
						break
					}
					if err != nil {
						t.Fatalf("name %d shift %d %s: %v", n, shift, name, err)
					}
					if tk.Kind() == Text {
						text.Write(tk.Text())
					}
				}
				if text.String() != want {
					t.Fatalf("name %d shift %d %s: text differs (len %d, want %d)", n, shift, name, text.Len(), len(want))
				}
			}
		}
		// small windows exercise the same cut on every offset of a short document
		for limit := 1; limit <= 12; limit++ {
			in := "<r>ab" + ref + "cd" + ref + "</r>"
			d := NewDecoderBytes([]byte(in), ents, SplitText(true))
			d.eng.textLimit = limit
			var text strings.Builder
			for {
				tk, err := d.ReadToken()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("name %d limit %d: %v", n, limit, err)
				}
				if tk.Kind() == Text {
					text.Write(tk.Text())
				}
			}
			if want := "ab" + value + "cd" + value; text.String() != want {
				t.Fatalf("name %d limit %d: %q, want %q", n, limit, text.String(), want)
			}
		}
	}
}

// The Encoder does not write what the default Decoder rejects.
func TestStrictEncoderProcInst(t *testing.T) {
	type step struct {
		target, inst string
	}
	for _, c := range []struct {
		name  string
		steps []step
		pre   func(e *Encoder) error // runs before the steps
		bad   bool                   // the last step must fail, the others succeed
	}{
		{name: "declaration first", steps: []step{{"xml", `version="1.0"`}}},
		{name: "declaration with encoding and standalone", steps: []step{{"xml", `version="1.0" encoding="UTF-8" standalone="yes"`}}},
		{name: "declaration with leading space", steps: []step{{"xml", ` version="1.0"`}}},
		{name: "declaration then stylesheet", steps: []step{{"xml", `version="1.0"`}, {"xml-stylesheet", `href="a"`}}},
		{name: "second declaration", steps: []step{{"xml", `version="1.0"`}, {"xml", `version="1.0"`}}, bad: true},
		{name: "declaration after comment", steps: []step{{"xml", `version="1.0"`}}, bad: true, pre: func(e *Encoder) error { return e.Comment([]byte("c")) }},
		{name: "declaration after root", steps: []step{{"xml", `version="1.0"`}}, bad: true, pre: func(e *Encoder) error {
			e.BeginElement(Name{Local: "r"})
			return e.EndElement()
		}},
		{name: "declaration inside root", steps: []step{{"xml", `version="1.0"`}}, bad: true, pre: func(e *Encoder) error { return e.BeginElement(Name{Local: "r"}) }},
		{name: "XML", steps: []step{{"XML", `version="1.0"`}}, bad: true},
		{name: "Xml", steps: []step{{"Xml", `x`}}, bad: true},
		{name: "xMl inside root", steps: []step{{"xMl", `x`}}, bad: true, pre: func(e *Encoder) error { return e.BeginElement(Name{Local: "r"}) }},
		{name: "colon", steps: []step{{"a:b", `x`}}, bad: true},
		{name: "colon xml", steps: []step{{"xml:b", `x`}}, bad: true},
		{name: "xml-stylesheet", steps: []step{{"xml-stylesheet", `href="a"`}}},
		{name: "xmlfoo", steps: []step{{"xmlfoo", `x`}}},
		{name: "no version", steps: []step{{"xml", `encoding="UTF-8"`}}, bad: true},
		{name: "empty declaration", steps: []step{{"xml", ``}}, bad: true},
		{name: "version 1.1", steps: []step{{"xml", `version="1.1"`}}, bad: true},
		{name: "standalone maybe", steps: []step{{"xml", `version="1.0" standalone="maybe"`}}, bad: true},
		{name: "bad encoding name", steps: []step{{"xml", `version="1.0" encoding="a b"`}}, bad: true},
		{name: "wrong order", steps: []step{{"xml", `standalone="no" version="1.0"`}}, bad: true},
		{name: "junk", steps: []step{{"xml", `version="1.0" junk`}}, bad: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			var b bytes.Buffer
			e := NewEncoder(&b)
			if c.pre != nil {
				if err := c.pre(e); err != nil {
					t.Fatalf("setup: %v", err)
				}
			}
			var err error
			for i, s := range c.steps {
				err = e.ProcInst(s.target, []byte(s.inst))
				if i < len(c.steps)-1 && err != nil {
					t.Fatalf("step %d: %v", i, err)
				}
			}
			if c.bad {
				if err == nil {
					t.Fatalf("accepted; wrote %q", b.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("rejected: %v", err)
			}
			e.BeginElement(Name{Local: "r"})
			e.EndElement()
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			// what the encoder wrote parses with the default profile
			if _, err := confRead(t, b.String()); err != nil {
				t.Fatalf("Decoder rejects the output %q: %v", b.String(), err)
			}
		})
	}
	// LegacyOutput follows encoding/xml and checks nothing
	var b bytes.Buffer
	e := NewEncoder(&b, xmlopts.LegacyOutput(true))
	if err := e.ProcInst("a:b", []byte("x")); err != nil {
		t.Fatalf("LegacyOutput rejects a colon target: %v", err)
	}
	if err := e.ProcInst("xml", []byte("anything")); err != nil {
		t.Fatalf("LegacyOutput rejects a declaration: %v", err)
	}
}

// A UTF-8 byte order mark is an encoding signature: the default profile reports
// no token for it, and Permissive (like encoding/xml) keeps it as text.
func TestStrictByteOrderMark(t *testing.T) {
	const bom = "\ufeff"
	for _, in := range []string{bom + "<r/>", bom + "<?xml version=\"1.0\"?>\n<r/>", bom + "\n<r/>"} {
		toks, err := confRead(t, in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		for _, tk := range toks {
			if strings.Contains(tk.text, bom) {
				t.Fatalf("%q: token %v holds the byte order mark", in, tk)
			}
		}
		d := NewDecoderBytes([]byte(in))
		tk, err := d.ReadToken()
		if err != nil || d.InputOffset() < 3 || tk.Kind() == Text && strings.HasPrefix(string(tk.Text()), bom) {
			t.Fatalf("%q: first token %v, offset %d, %v", in, tk.Kind(), d.InputOffset(), err)
		}
		perm, err := confRead(t, in, Permissive(true))
		if err != nil || len(perm) == 0 || perm[0].kind != Text || !strings.HasPrefix(perm[0].text, bom) {
			t.Fatalf("%q: Permissive tokens %v, %v", in, perm, err)
		}
	}
	// the mark is only special at the start of a stream; Reset forgets it
	d := NewDecoderBytes([]byte(bom + `<?xml version="1.0"?><r/>`))
	if _, err := readAll(t, d); err != nil {
		t.Fatal(err)
	}
	d.ResetBytes([]byte(` <?xml version="1.0"?><r/>`))
	if _, err := readAll(t, d); err == nil {
		t.Fatal("declaration after space accepted after a Reset that followed a byte order mark")
	}
	d.Reset(iotest.OneByteReader(strings.NewReader(bom + `<?xml version="1.0"?><r/>`)))
	if _, err := readAll(t, d); err != nil {
		t.Fatal(err)
	}
}
