package xml

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	legacy "github.com/go-xml-experiment/xml/internal/legacyxml"
)

// Value types that exercise the encoder and decoder paths: attributes,
// character data, CDATA, comments, innerxml, namespaces, paths, any, omitempty.
// Every type is declared once and used with both implementations (the struct
// tags are the same for both packages).

type diffAttrs struct {
	XMLName struct{} `xml:"item"`
	A       string   `xml:"a,attr"`
	B       string   `xml:"b,attr,omitempty"`
	NS      string   `xml:"urn:x ns,attr"`
	Text    string   `xml:",chardata"`
}

type diffMixed struct {
	XMLName struct{}    `xml:"urn:y root"`
	ID      string      `xml:"id,attr"`
	Title   string      `xml:"head>title"`
	Sub     string      `xml:"head>sub,omitempty"`
	Body    string      `xml:",cdata"`
	Note    string      `xml:",comment"`
	Items   []string    `xml:"item"`
	Kids    []diffAttrs `xml:"kid"`
	Raw     string      `xml:",innerxml"`
}

type diffAny struct {
	XMLName struct{}    `xml:"any"`
	N       int         `xml:"n"`
	F       float64     `xml:"f,attr"`
	Ok      bool        `xml:"ok"`
	Rest    []diffAttrs `xml:",any"`
}

func marshalBoth(t *testing.T, v any, prefix, indent string) (string, string) {
	var a, b []byte
	var ea, eb error
	if indent == "" && prefix == "" {
		a, ea = legacy.Marshal(v)
		b, eb = Marshal(v)
	} else {
		a, ea = legacy.MarshalIndent(v, prefix, indent)
		b, eb = MarshalIndent(v, prefix, indent)
	}
	if fmt.Sprint(ea) != fmt.Sprint(eb) {
		t.Fatalf("Marshal(%#v) error differs: legacy %v, new %v", v, ea, eb)
	}
	return string(a), string(b)
}

func FuzzMarshalAgainstLegacy(f *testing.F) {
	f.Add("v", "w", "x", "text<&>\"'", "title", "sub", "body]]>x", "c-", "i1", "raw<r/>", 3, 1.5, true, byte(0))
	f.Add("", "", "", "", "", "", "", "", "", "", 0, 0.0, false, byte(1))
	f.Add("a\nb", "\t", "é한😀", "\r\n", "t", "", "]]>", "--", "<", "<x>", -1, -2e10, true, byte(2))
	f.Fuzz(func(t *testing.T, s1, s2, s3, s4, s5, s6, s7, s8, s9, s10 string, n int, fl float64, ok bool, mode byte) {
		for _, v := range []string{s1, s2, s3, s4, s5, s6, s7, s8, s9, s10} {
			if !utf8.ValidString(v) {
				// two errors in one text run (bad UTF-8 and something else) may be
				// reported in a different order; see legacy_diff_test.go
				t.Skip()
			}
		}
		prefix, indent := "", ""
		if mode&1 != 0 {
			prefix, indent = ">", "  "
		}
		values := []any{
			&diffAttrs{A: s1, B: s2, NS: s3, Text: s4},
			&diffMixed{ID: s1, Title: s5, Sub: s6, Body: s7, Note: s8, Items: []string{s9, s1}, Kids: []diffAttrs{{A: s2, Text: s3}}, Raw: s10},
			&diffAny{N: n, F: fl, Ok: ok, Rest: []diffAttrs{{A: s1, Text: s2}}},
			diffAttrs{A: s1},
		}
		for _, v := range values {
			a, b := marshalBoth(t, v, prefix, indent)
			if a != b {
				t.Fatalf("Marshal(%#v)\nlegacy %q\n   new %q", v, a, b)
			}
			// decode what was written with both and compare the results
			typ := reflect.TypeOf(v)
			if typ.Kind() == reflect.Pointer {
				typ = typ.Elem()
			}
			ta, tb := reflect.New(typ), reflect.New(typ)
			ea := legacy.Unmarshal([]byte(a), ta.Interface())
			eb := Unmarshal([]byte(a), tb.Interface())
			if ea != nil && strings.Contains(ea.Error(), "invalid XML name") {
				t.Skip() // xmltext accepts the XML 1.0 fifth-edition name characters
			}
			if fmt.Sprint(ea) != fmt.Sprint(eb) {
				t.Fatalf("Unmarshal(%q) error differs: legacy %v, new %v", a, ea, eb)
			}
			if !reflect.DeepEqual(ta.Interface(), tb.Interface()) {
				t.Fatalf("Unmarshal(%q) value differs:\nlegacy %#v\n   new %#v", a, ta.Interface(), tb.Interface())
			}
		}
	})
}
