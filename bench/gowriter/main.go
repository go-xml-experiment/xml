// Writer benchmark (plan 7.E writer row): the input document is read once into
// an in-memory event list (not timed), then the list is written to an
// in-memory sink. Every engine writes with its own native escaping and empty
// element form, so output bytes differ; "verify" re-reads an output with the
// strict xmltext reader and requires the same events and checksum as the input.
//
//	gowriter ENGINE FILE WARMUPS RUNS [DUMPFILE]   engines: xmltext, xmltext-legacy, std
//	gowriter verify INPUT OUTPUT
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"runtime/pprof"
	"strconv"
	"time"
	"unicode/utf8"

	xml "github.com/go-xml-experiment/xml/internal/legacyxml"
	"github.com/go-xml-experiment/xml/internal/xmlopts"
	"github.com/go-xml-experiment/xml/xmltext"
)

type kv struct{ name, value string }

type event struct {
	kind  xmltext.Kind
	name  string
	attrs []kv
	text  string
}

func load(data []byte) ([]event, error) {
	d := xmltext.NewDecoderBytes(data)
	var out []event
	for {
		t, err := d.ReadToken()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, err
		}
		switch t.Kind() {
		case xmltext.StartElement:
			e := event{kind: xmltext.StartElement, name: t.Name().Local}
			for it := t.Attrs(); it.Next(); {
				e.attrs = append(e.attrs, kv{it.Name().Local, string(it.Value())})
			}
			out = append(out, e)
		case xmltext.EndElement:
			out = append(out, event{kind: xmltext.EndElement, name: t.Name().Local})
		case xmltext.Text, xmltext.CData:
			out = append(out, event{kind: xmltext.Text, text: string(t.Text())})
		}
	}
}

func writeXMLText(w *bytes.Buffer, evs []event, opts ...xmltext.Options) error {
	e := xmltext.NewEncoder(w, opts...)
	return writeWith(e, evs)
}

func writeWith(e *xmltext.Encoder, evs []event) error {
	for i := range evs {
		ev := &evs[i]
		var err error
		switch ev.kind {
		case xmltext.StartElement:
			err = e.BeginElement(xmltext.Name{Local: ev.name})
			for _, a := range ev.attrs {
				if err == nil {
					err = e.Attr(xmltext.Name{Local: a.name}, a.value)
				}
			}
		case xmltext.EndElement:
			err = e.EndElement()
		case xmltext.Text:
			err = e.Text(ev.text)
		}
		if err != nil {
			return err
		}
	}
	return e.Close()
}

func writeStd(w *bytes.Buffer, evs []event) error {
	e := xml.NewEncoder(w)
	for i := range evs {
		ev := &evs[i]
		var err error
		switch ev.kind {
		case xmltext.StartElement:
			st := xml.StartElement{Name: xml.Name{Local: ev.name}}
			if len(ev.attrs) > 0 {
				st.Attr = make([]xml.Attr, len(ev.attrs))
				for j, a := range ev.attrs {
					st.Attr[j] = xml.Attr{Name: xml.Name{Local: a.name}, Value: a.value}
				}
			}
			err = e.EncodeToken(st)
		case xmltext.EndElement:
			err = e.EncodeToken(xml.EndElement{Name: xml.Name{Local: ev.name}})
		case xmltext.Text:
			err = e.EncodeToken(xml.CharData(ev.text))
		}
		if err != nil {
			return err
		}
	}
	return e.Close()
}

func write(engine string, w *bytes.Buffer, evs []event) error {
	switch engine {
	case "xmltext":
		return writeXMLText(w, evs)
	case "xmltext-legacy":
		return writeXMLText(w, evs, xmlopts.LegacyOutput(true))
	case "std":
		return writeStd(w, evs)
	}
	return fmt.Errorf("unknown engine %q", engine)
}

// ---- reader checksum (same consumer as bench/gobench, for verify) ----

func foldBytes(seed uint32, value []byte) uint32 {
	for len(value) != 0 {
		r, n := utf8.DecodeRune(value)
		value = value[n:]
		if r > 0xffff {
			r -= 0x10000
			seed = (seed*31+uint32(0xd800+(r>>10)))*31 + uint32(0xdc00+(r&0x3ff))
		} else {
			seed = seed*31 + uint32(r)
		}
	}
	return seed
}

func xmlSpace(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }

func checksum(data []byte) (uint64, int32, error) {
	d := xmltext.NewDecoderBytes(data)
	var events uint64
	var sum uint32
	var pending []byte
	flush := func() {
		s, e := 0, len(pending)
		for s < e && xmlSpace(pending[s]) {
			s++
		}
		for e > s && xmlSpace(pending[e-1]) {
			e--
		}
		if s < e {
			events++
			sum = foldBytes(sum*31+'T', pending[s:e])
		}
		pending = pending[:0]
	}
	for {
		t, err := d.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, 0, err
		}
		switch t.Kind() {
		case xmltext.StartElement:
			flush()
			events++
			sum = foldBytes(sum*31+'S', []byte(t.Name().Local))
			it := t.Attrs()
			sum = (sum ^ uint32(it.Len())) * 16777619
			for it.Next() {
				sum = foldBytes(sum, []byte(it.Name().Local))
				sum = foldBytes(sum, it.Value())
			}
		case xmltext.EndElement:
			flush()
			events++
			sum = foldBytes(sum*31+'E', []byte(t.Name().Local))
		case xmltext.Text, xmltext.CData:
			pending = append(pending, t.Text()...)
		}
	}
	flush()
	return events, int32(sum), nil
}

func run() error {
	if len(os.Args) == 4 && os.Args[1] == "verify" {
		in, err := os.ReadFile(os.Args[2])
		if err != nil {
			return err
		}
		out, err := os.ReadFile(os.Args[3])
		if err != nil {
			return err
		}
		ie, ic, err := checksum(in)
		if err != nil {
			return err
		}
		oe, oc, err := checksum(out)
		if err != nil {
			return fmt.Errorf("output does not parse: %w", err)
		}
		if ie != oe || ic != oc {
			return fmt.Errorf("round trip differs: input events=%d checksum=%d, output events=%d checksum=%d", ie, ic, oe, oc)
		}
		fmt.Printf("ok events=%d checksum=%d output=%d bytes\n", oe, oc, len(out))
		return nil
	}
	if len(os.Args) < 5 {
		return fmt.Errorf("usage: gowriter ENGINE FILE WARMUPS RUNS [DUMPFILE] | verify INPUT OUTPUT")
	}
	engine, path := os.Args[1], os.Args[2]
	warm, err := strconv.Atoi(os.Args[3])
	if err != nil {
		return err
	}
	runs, err := strconv.Atoi(os.Args[4])
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	evs, err := load(data)
	if err != nil {
		return err
	}
	var sink bytes.Buffer
	sink.Grow(2 * len(data))
	once := func() error { sink.Reset(); return write(engine, &sink, evs) }
	if err := once(); err != nil {
		return err
	}
	outBytes := sink.Len()
	if len(os.Args) == 6 {
		if err := os.WriteFile(os.Args[5], sink.Bytes(), 0o644); err != nil {
			return err
		}
	}
	started := time.Now()
	for i := 0; i < warm || time.Since(started) < time.Second; i++ {
		if err := once(); err != nil {
			return err
		}
	}
	type sample struct {
		Seconds    float64 `json:"seconds"`
		Iterations int     `json:"iterations"`
	}
	var samples []sample
	for range runs {
		t, n := time.Now(), 0
		for n == 0 || time.Since(t) < 100*time.Millisecond {
			if err := once(); err != nil {
				return err
			}
			n++
		}
		samples = append(samples, sample{time.Since(t).Seconds() / float64(n), n})
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Engine      string   `json:"engine"`
		Version     string   `json:"version"`
		InputBytes  int      `json:"inputBytes"`
		OutputBytes int      `json:"outputBytes"`
		Events      int      `json:"events"`
		Samples     []sample `json:"samples"`
	}{engine, runtime.Version(), len(data), outBytes, len(evs), samples})
}

func main() {
	if pf := os.Getenv("XML_BENCH_PPROF"); pf != "" { // development knob: CPU profile of the whole run
		f, _ := os.Create(pf)
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}
	if err := run(); err != nil {
		pprof.StopCPUProfile()
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
