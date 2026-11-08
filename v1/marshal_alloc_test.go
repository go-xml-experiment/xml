// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"github.com/go-xml-experiment/xml/internal/race"
	"github.com/go-xml-experiment/xml/internal/testenv"
	"testing"
)

// Marshal of a pointer allocates only the result it returns: method lookup,
// attributes and formatted text use cached or pooled storage.
func TestMarshalAllocations(t *testing.T) {
	testenv.SkipIfOptimizationOff(t)
	if race.Enabled {
		t.Skip("skipping malloc count under race detector: sync.Pool drops items")
	}
	type nums struct {
		XMLName Name    `xml:"n"`
		F       float64 `xml:"f,attr"`
		I       int     `xml:"i,attr"`
		B       []byte  `xml:"b,attr"`
		E       float32 `xml:"x>y>e"`
		G       uint    `xml:"x>y>g"`
	}
	for name, v := range map[string]any{
		"atom":    atomValue,
		"numbers": &nums{F: 2.5, I: 12345, B: []byte("bytes"), E: 0.125, G: 7},
	} {
		Marshal(v) // warm up the caches and pools
		if n := testing.AllocsPerRun(100, func() { Marshal(v) }); n > 1 {
			t.Errorf("%s: %v allocations, want at most 1", name, n)
		}
	}
}

type appendAndMarshalText struct{}

func (appendAndMarshalText) MarshalText() ([]byte, error) { return []byte("marshal"), nil }

func (appendAndMarshalText) AppendText(b []byte) ([]byte, error) { return append(b, "append"...), nil }

// Like encoding/json/v2, the xmlv2 Marshal calls AppendText when a type has
// both methods. The encoding package requires them to give the same text;
// this case differs on purpose to show which one runs.
func TestMarshalPrefersAppendText(t *testing.T) {
	type doc struct {
		XMLName Name                 `xml:"d"`
		A       appendAndMarshalText `xml:"a,attr"`
		E       appendAndMarshalText `xml:"e"`
	}
	got, err := Marshal(&doc{})
	if err != nil {
		t.Fatal(err)
	}
	if want := `<d a="append"><e>append</e></d>`; string(got) != want {
		t.Errorf("got %s, want %s", got, want)
	}
}
