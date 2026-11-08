// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"io"
	"strings"
	"testing"

	"github.com/go-xml-experiment/xml/internal/xmlopts"
)

func TestLegacyReferenceScanNoAllocations(t *testing.T) {
	for _, entities := range []map[string]string{nil, {}, {"custom": "replacement"}} {
		for _, input := range []string{
			"plain text without references",
			"&amp;&lt;&gt;&apos;&quot;&#65;&#x41;",
			"<!--&custom;-->",
			"<![CDATA[&custom;]]>",
			"<?pi &custom;?>",
		} {
			strict := false
			var charset func(string, io.Reader) (io.Reader, error)
			settings := xmlopts.LegacyDecoder{Strict: &strict, Entity: &entities, CharsetReader: &charset}
			d := NewDecoderBytes([]byte(input), Permissive(true), xmlopts.NewOption(xmlopts.Options{LegacyDecoder: &settings}))
			allocs := testing.AllocsPerRun(100, func() {
				d.beginLegacy()
				d.beforeLegacyRead()
			})
			if allocs != 0 || d.legacy != nil {
				t.Fatalf("input %q, map size %d: allocations=%v, ledger=%v", input, len(entities), allocs, d.legacy)
			}
		}
	}
}

func TestLegacyReferenceSnapshotReset(t *testing.T) {
	const input = "<r>&e;|&e;</r>"
	for _, release := range []bool{false, true} {
		for _, chunk := range []int{1, 7} {
			strict := true
			entities := map[string]string{"e": "before"}
			var charset func(string, io.Reader) (io.Reader, error)
			settings := xmlopts.LegacyDecoder{Strict: &strict, Entity: &entities, CharsetReader: &charset}
			opts := []Options{Permissive(true), xmlopts.NewOption(xmlopts.Options{LegacyDecoder: &settings})}
			offset, changeAt := 0, strings.IndexByte(input, '|')+1
			r := readerFunc(func(p []byte) (int, error) {
				if offset == len(input) {
					return 0, io.EOF
				}
				n := min(len(p), chunk, len(input)-offset)
				if offset < changeAt-1 {
					n = min(n, changeAt-1-offset)
				} else if offset < changeAt {
					n = min(n, changeAt-offset)
				}
				copy(p, input[offset:offset+n])
				offset += n
				if offset == changeAt {
					entities["e"] = "after"
				}
				return n, nil
			})
			d := NewDecoder(r, opts...)
			d.StartCapture()
			if _, err := d.ReadToken(); err != nil {
				t.Fatal(err)
			}
			token, err := d.ReadToken()
			if err != nil || string(token.Text()) != "before|after" {
				t.Fatalf("chunk=%d: text=%q, err=%v", chunk, token.Text(), err)
			}
			if d.legacy == nil || len(d.legacy.refs) == 0 {
				t.Fatal("test did not create a reference snapshot")
			}
			if raw := string(d.Captured(0, d.CaptureLen())); raw != "<r>&e;|&e;" {
				t.Fatalf("capture changed: %q", raw)
			}
			if release {
				d.Release()
				if d.legacy != nil {
					t.Fatal("Release retained reference snapshots")
				}
			}
			entities["e"] = "fresh"
			d.ResetBytes([]byte(input), opts...)
			if _, err := d.ReadToken(); err != nil {
				t.Fatal(err)
			}
			token, err = d.ReadToken()
			if err != nil || string(token.Text()) != "fresh|fresh" || d.legacy != nil {
				t.Fatalf("release=%v: stale snapshot after Reset: text=%q, err=%v", release, token.Text(), err)
			}
		}
	}
}
