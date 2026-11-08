// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"fmt"
	"io"
	"maps"
	"reflect"
	"strings"
	"testing"
)

// Both reader forms return one byte at a time, so callbacks run at the same
// consumed byte in the oracle and adapter, regardless of their buffering.
type settingsCallbackReader struct {
	input  string
	offset int
	at     int
	change func()
	calls  int
}

func (r *settingsCallbackReader) ReadByte() (byte, error) {
	if r.offset == len(r.input) {
		return 0, io.EOF
	}
	b := r.input[r.offset]
	r.offset++
	if r.offset == r.at {
		r.calls++
		r.change()
	}
	return b, nil
}

func (r *settingsCallbackReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	b, err := r.ReadByte()
	if err != nil {
		return 0, err
	}
	p[0] = b
	return 1, nil
}

type settingsCallbackPosition interface {
	InputOffset() int64
	InputPos() (int, int)
}

func settingsCallbackRecord(d settingsCallbackPosition, token string, err error) string {
	line, column := d.InputPos()
	return fmt.Sprintf("%s err=%s @%d:%d:%d", token, errString(err), d.InputOffset(), line, column)
}

// Include the terminal call: errors and EOF have observable positions too.
func settingsCallbackTrace(t *testing.T, d settingsCallbackPosition, next func() (string, error)) []string {
	t.Helper()
	var trace []string
	for range 100 {
		token, err := next()
		trace = append(trace, settingsCallbackRecord(d, token, err))
		if err != nil {
			return trace
		}
	}
	t.Fatal("decoder did not reach EOF or an error")
	return nil
}

func TestSettingsReaderCallbacksLegacy(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		marker  string
		strict  bool
		initial map[string]string
		after   map[string]string
		loose   bool
		inPlace bool
	}{
		{
			name: "replace-entity-during-text-peek", input: "<r>" + strings.Repeat("x", 40<<10) + "&old;|&new;|&old;</r>", marker: "&new;", strict: true,
			initial: map[string]string{"old": "before"}, after: map[string]string{"old": "after", "new": "added"},
		},
		{
			name: "install-entity-mid-reference", input: "<r>&new;</r>", marker: "&new;", strict: true,
			after: map[string]string{"new": "added"},
		},
		{
			name: "relax-unquoted-attribute", input: "<r a=value/>", marker: "a=v", strict: true, loose: true,
		},
		{
			name: "relax-valueless-attribute", input: "<r a />", marker: "a /", strict: true, loose: true,
		},
		{
			name: "replace-text-entity-mid-token", input: "<r>\n&old;|&new;|&old;</r>", marker: "&new;", strict: true,
			initial: map[string]string{"old": "before"}, after: map[string]string{"old": "after", "new": "added"},
		},
		{
			name: "mutate-text-entity-after-first-reference", input: "<r>\n&old;|&old;</r>", marker: "|", strict: true,
			initial: map[string]string{"old": "before"}, after: map[string]string{"old": "after"}, inPlace: true,
		},
		{
			name: "replace-attribute-entity-mid-token", input: "<r\n a=\"&old;|&new;|&old;\"/>", marker: "&new;", strict: true,
			initial: map[string]string{"old": "before"}, after: map[string]string{"old": "after", "new": "added"},
		},
		{
			name: "remove-entity-and-relax-text", input: "<r>\n&old;|&old;|&missing;</r>", marker: "|&old;", strict: true,
			initial: map[string]string{"old": "before"}, loose: true,
		},
		{
			name: "relax-attribute-mid-entity", input: "<r\n a=\"&missing;\"/>", marker: "&missing;", strict: true, loose: true,
		},
		{
			name: "tighten-text-mid-entity", input: "<r>\n&old;|&missing;</r>", marker: "&missing;",
			initial: map[string]string{"old": "before"},
		},
		{
			name: "tighten-attribute-mid-entity", input: "<r\n a=\"&missing;\"/>", marker: "&missing;",
		},
	} {
		for _, byteReader := range []bool{false, true} {
			for _, raw := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/byte-reader=%t/raw=%t", tc.name, byteReader, raw), func(t *testing.T) {
					at := strings.Index(tc.input, tc.marker) + len(tc.marker)
					gr := &settingsCallbackReader{input: tc.input, at: at}
					var gsrc io.Reader = gr
					if !byteReader {
						gsrc = struct{ io.Reader }{gr}
					}
					got := NewDecoder(gsrc)
					got.Strict = tc.strict
					// Never share mutable maps between decoders.
					got.Entity = maps.Clone(tc.initial)
					change := func(entity *map[string]string, strict *bool) {
						if tc.inPlace {
							for k, v := range tc.after {
								(*entity)[k] = v
							}
						} else {
							*entity = maps.Clone(tc.after)
						}
						*strict = !tc.loose
					}
					gr.change = func() { change(&got.Entity, &got.Strict) }
					wtrace := fixedValue(t.Name(), "*xml.Decoder", 0)
					gtrace := settingsCallbackTrace(t, got, func() (string, error) {
						var token Token
						var err error
						if raw {
							token, err = got.RawToken()
						} else {
							token, err = got.Token()
						}
						return renderNew(token), err
					})
					if gr.calls != 1 {
						t.Fatalf("callback count: %d, want one", gr.calls)
					}
					if !reflect.DeepEqual(gtrace, wtrace) {
						t.Fatalf("legacy: %q\n   new: %q", wtrace, gtrace)
					}
				})
			}
		}
	}
}

func TestSettingsUnknownEntityNotAddedRetroactively(t *testing.T) {
	for _, input := range []string{`<r>&new;|&new;</r>`, `<r a="&new;|&new;"/>`} {
		for _, initial := range []map[string]string{nil, {}, {"other": "unchanged"}} {
			r := &settingsCallbackReader{input: input, at: strings.IndexByte(input, '|') + 1}
			d := NewDecoder(r)
			d.Entity = maps.Clone(initial)
			r.change = func() {
				if d.Entity == nil {
					d.Entity = make(map[string]string)
				}
				d.Entity["new"] = "added"
			}
			var value string
			if err := d.Decode(&value); err == nil || !strings.Contains(err.Error(), "invalid character entity") {
				t.Fatalf("input %q, initial map %v: Decode = %q, %v; first reference must fail", input, initial, value, err)
			}
		}
	}
}

func TestSettingsCharsetCallbackLegacy(t *testing.T) {
	const input = "<?xml version=\"1.0\" encoding=\"callback\"?><r>\n&e;|&missing;</r>"
	got := NewDecoder(strings.NewReader(input))
	gcalls := 0
	got.CharsetReader = func(label string, r io.Reader) (io.Reader, error) {
		gcalls++
		if label != "callback" {
			t.Fatalf("new charset label = %q", label)
		}
		got.Entity, got.Strict = map[string]string{"e": "converted"}, false
		return r, nil
	}
	wtrace := fixedValue(t.Name(), "*xml.Decoder", 0)
	gtrace := settingsCallbackTrace(t, got, func() (string, error) {
		token, err := got.Token()
		return renderNew(token), err
	})
	if gcalls != 1 || !reflect.DeepEqual(gtrace, wtrace) {
		t.Fatalf("want %q, got %q (%d callbacks)", wtrace, gtrace, gcalls)
	}
}

type settingsCallbackBody[S any] struct {
	Raw  string `xml:",innerxml"`
	Text string `xml:",chardata"`
	Skip S      `xml:"skip"`
}

type settingsCallbackHook struct {
	Body      settingsCallbackBody[settingsCallbackSkip]
	NoConsume bool
}

func (h *settingsCallbackHook) UnmarshalXML(d *Decoder, start StartElement) error {
	d.Entity, d.Strict = map[string]string{"e": "hook"}, false
	if h.NoConsume {
		return nil
	}
	return d.DecodeElement(&h.Body, &start)
}

type settingsCallbackSkip struct{ Called bool }

func (s *settingsCallbackSkip) UnmarshalXML(d *Decoder, start StartElement) error {
	s.Called = true
	d.Entity, d.Strict = map[string]string{"e": "skipped"}, true
	return d.Skip()
}

func TestSettingsUnmarshalCallbacksLegacy(t *testing.T) {
	const inner = "<hook>\n&e;|&missing;<skip><x a=\"&e;\"/></skip>|&e;</hook><tail>&e;</tail>"
	const input = "<root>" + inner + "</root><after/>"
	d := NewDecoder(strings.NewReader(input))
	var got struct {
		Raw  string               `xml:",innerxml"`
		Hook settingsCallbackHook `xml:"hook"`
		Tail string               `xml:"tail"`
	}
	err := d.Decode(&got)
	if err != nil || got.Raw != inner || got.Hook.Body.Raw != "\n&e;|&missing;<skip><x a=\"&e;\"/></skip>|&e;" || got.Hook.Body.Text != "\nhook|&missing;|skipped" || !got.Hook.Body.Skip.Called || got.Tail != "skipped" {
		t.Fatalf("Decode: %+v (%v)", got, err)
	}
	trace := settingsCallbackTrace(t, d, func() (string, error) { tk, e := d.Token(); return renderNew(tk), e })
	want := fixedValue(t.Name(), "*xml.Decoder", 0)
	if !reflect.DeepEqual(trace, want) {
		t.Fatalf("got %q, want %q", trace, want)
	}
}
func TestSettingsUnmarshalCallbackMustConsume(t *testing.T) {
	const input = "<hook>\n&e;</hook><after/>"
	d := NewDecoder(strings.NewReader(input))
	got := settingsCallbackHook{NoConsume: true}
	err := d.Decode(&got)
	const want = "xml: (*xml.settingsCallbackHook).UnmarshalXML did not consume entire <hook> element"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v, want %q", err, want)
	}
	line, col := d.InputPos()
	if d.InputOffset() != 6 || line != 1 || col != 7 {
		t.Fatalf("position %d:%d:%d", d.InputOffset(), line, col)
	}
}
