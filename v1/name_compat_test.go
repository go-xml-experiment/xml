// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestLegacyNameCompatibility(t *testing.T) {
	for _, name := range []string{"\u0132", "\u0140", "\u2070", "\U00010000", "é", "한", "a\u0300"} {
		t.Run(fmt.Sprintf("%U", []rune(name)), func(t *testing.T) {
			inputs := []string{
				"<" + name + "/>",
				"<r " + name + "=\"v\"/>",
				"<r></" + name + ">",
				"<?" + name + "?>",
				"<r>&" + name + ";</r>",
				"<r k=\"&" + name + ";\"/>",
				"<" + name + " k=",
				"<r " + name + "=",
				"</" + name + " x",
				"<?" + name + " x",
			}
			for _, strict := range []bool{true, false} {
				for _, raw := range []bool{false, true} {
					cfg := tokenCfg{strict: strict, raw: raw, entity: map[string]string{name: "replacement"}}
					for _, in := range inputs {
						if diff := compareTokens(in, cfg); diff != "" {
							t.Errorf("strict=%v raw=%v input=%q: %s", strict, raw, in, diff)
						}
					}
				}
				for _, in := range []string{
					"<" + name + " " + name + "=\"v\">body</" + name + ">",
					"<?" + name + "?><r/>",
					"<r>&" + name + ";</r>",
				} {
					var got struct {
						XMLName Name
						Text    string `xml:",chardata"`
					}
					d := NewDecoder(strings.NewReader(in))
					d.Strict, d.Entity = strict, map[string]string{name: "replacement"}
					err := d.Decode(&got)
					nameValid := name == "é" || name == "한" || name == "a\u0300"
					valid := nameValid || !strict && strings.HasPrefix(in, "<r>&")
					if (err == nil) != valid {
						t.Errorf("Decode %q: error %v, accepted=%v", in, err, valid)
					}
					if valid {
						text := "body"
						local := name
						if strings.HasPrefix(in, "<?") {
							local, text = "r", ""
						} else if strings.HasPrefix(in, "<r>") {
							local, text = "r", "replacement"
							if !nameValid {
								text = "&" + name + ";"
							}
						}
						if got.XMLName != (Name{Local: local}) || got.Text != text {
							t.Errorf("Decode %q: got %+v", in, got)
						}
					}

				}
			}
		})
	}
}

func TestLegacyEntityNameCompatibility(t *testing.T) {
	for _, name := range []string{"1x", "a b", "ok", "é"} {
		for _, strict := range []bool{true, false} {
			for _, raw := range []bool{false, true} {
				cfg := tokenCfg{strict: strict, raw: raw, entity: map[string]string{name: "replacement"}}
				for _, in := range []string{"<r>&" + name + ";</r>", "<r k=\"&" + name + ";\"/>"} {
					if diff := compareTokens(in, cfg); diff != "" {
						t.Errorf("strict=%v raw=%v input=%q: %s", strict, raw, in, diff)
					}
				}
			}
		}
	}
}

func TestLegacyEncoderNameCompatibility(t *testing.T) {
	for _, name := range []string{"1x", "a b", "\u0132", "é"} {
		for _, pi := range []bool{false, true} {
			var out bytes.Buffer
			e := NewEncoder(&out)
			var err error
			want := "<" + name + " " + name + "=\"v\"></" + name + ">"
			if pi {
				err = e.EncodeToken(ProcInst{Target: name})
				want = "<?" + name + "?>"
			} else {
				err = e.EncodeToken(StartElement{Name: Name{Local: name}, Attr: []Attr{{Name: Name{Local: name}, Value: "v"}}})
				if err == nil {
					err = e.EncodeToken(EndElement{Name: Name{Local: name}})
				}
			}
			invalidPI := pi && name != "é"
			if invalidPI {
				if err == nil || err.Error() != "xml: EncodeToken of ProcInst with invalid Target" {
					t.Errorf("name %q: error %v", name, err)
				}
				continue
			}
			if err == nil {
				err = e.Flush()
			}
			if err != nil || out.String() != want {
				t.Errorf("name %q pi=%v: got %q (%v), want %q", name, pi, out.String(), err, want)
			}
		}
	}
}
