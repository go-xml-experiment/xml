package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	xml "github.com/go-xml-experiment/xml/internal/legacyxml"
	xmlv1 "github.com/go-xml-experiment/xml/v1"
	"github.com/go-xml-experiment/xml/xmltext"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"
	"unicode/utf8"
)

type result struct {
	Events   uint64 `json:"events"`
	Checksum int32  `json:"checksum"`
}

type sample struct {
	result
	Seconds        float64 `json:"seconds"`
	Iterations     int     `json:"iterations"`
	Allocations    uint64  `json:"allocations"`    // Total allocations across measured iterations.
	AllocatedBytes uint64  `json:"allocatedBytes"` // Total allocated bytes across measured iterations.
}

func foldRune(seed uint32, r rune) uint32 {
	if r <= 0xffff {
		return seed*31 + uint32(r)
	}
	r -= 0x10000
	return (seed*31+uint32(0xd800+(r>>10)))*31 + uint32(0xdc00+(r&0x3ff))
}

func foldString(seed uint32, value string) uint32 {
	for _, r := range value {
		seed = foldRune(seed, r)
	}
	return seed
}

func foldBytes(seed uint32, value []byte) uint32 {
	for len(value) != 0 {
		c := value[0]
		switch {
		case c < utf8.RuneSelf:
			// ASCII runs: fold four bytes per multiply (31^4 = 923521) so the
			// serial h*31+c dependency chain is a quarter as long; same result.
			// Only tried on an ASCII lead byte so non-ASCII text pays nothing.
			if len(value) >= 4 && binary.LittleEndian.Uint32(value)&0x80808080 == 0 {
				seed = seed*923521 + uint32(c)*29791 + uint32(value[1])*961 + uint32(value[2])*31 + uint32(value[3])
				value = value[4:]
				continue
			}
			seed = seed*31 + uint32(c)
			value = value[1:]
		case c < 0xE0 && len(value) >= 2: // input is already validated UTF-8
			seed = seed*31 + (uint32(c&0x1F)<<6 | uint32(value[1]&0x3F))
			value = value[2:]
		case c < 0xF0 && len(value) >= 3:
			seed = seed*31 + (uint32(c&0x0F)<<12 | uint32(value[1]&0x3F)<<6 | uint32(value[2]&0x3F))
			value = value[3:]
		default:
			r, n := utf8.DecodeRune(value)
			seed = foldRune(seed, r)
			value = value[n:]
		}
	}
	return seed
}

func xmlSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func consume(engine, path string, loaded []byte, mode string) (result, error) {
	data := loaded
	if mode == "file" {
		var err error
		data, err = os.ReadFile(path)
		if err != nil {
			return result{}, err
		}
	}
	switch engine {
	case "xmltext":
		return consumeXMLText(data)
	case "xmltext-reader":
		return consumeXMLTextReader(data)
	case "xmltext-reader-stream":
		return consumeXMLTextReaderStream(data)
	case "legacy":
		return consumeLegacy(data)
	case "v1-on-v2":
		return consumeV1(data)
	}
	return result{}, fmt.Errorf("unknown engine %q", engine)
}

type folder struct {
	events   uint64
	checksum uint32
	pending  []byte
}

func (f *folder) flush() {
	start, end := 0, len(f.pending)
	for start < end && xmlSpace(f.pending[start]) {
		start++
	}
	for end > start && xmlSpace(f.pending[end-1]) {
		end--
	}
	if start < end {
		f.events++
		f.checksum = foldBytes(f.checksum*31+'T', f.pending[start:end])
	}
	f.pending = f.pending[:0]
}

// Woodstox getName().toString() is "{uri}local" or "local"; the checksum
// here folds only the local name, as the original Go reader did.
func (f *folder) start(local []byte, nattr int) {
	f.flush()
	f.events++
	f.checksum = foldBytes(f.checksum*31+'S', local)
	f.checksum = (f.checksum ^ uint32(nattr)) * 16777619
}

func (f *folder) end(local []byte) {
	f.flush()
	f.events++
	f.checksum = foldBytes(f.checksum*31+'E', local)
}

func consumeXMLText(data []byte) (result, error) {
	return consumeDecoder(xmltext.NewDecoderBytes(data))
}

func consumeXMLTextReader(data []byte) (result, error) {
	return consumeDecoder(xmltext.NewDecoder(bufio.NewReaderSize(bytes.NewReader(data), 4096)))
}

// consumeXMLTextReaderStream produces the same events and checksum as
// consumeXMLTextReader but folds each streamed Text piece as it arrives, holding
// only a trailing whitespace run, so memory stays bounded. It is reported
// separately from the coalescing consumer, never in its place.
func consumeXMLTextReaderStream(data []byte) (result, error) {
	d := xmltext.NewDecoder(bufio.NewReaderSize(bytes.NewReader(data), 4096))
	var f folder
	var started bool
	var held []byte
	reset := func() { started, held = false, held[:0] }
	for {
		t, err := d.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result{}, err
		}
		switch t.Kind() {
		case xmltext.StartElement:
			reset()
			it := t.Attrs()
			f.start([]byte(t.Name().Local), it.Len())
			for it.Next() {
				f.checksum = foldString(f.checksum, it.Name().Local)
				f.checksum = foldBytes(f.checksum, it.Value())
			}
		case xmltext.EndElement:
			reset()
			f.end([]byte(t.Name().Local))
		case xmltext.Text, xmltext.CData:
			piece := t.Text()
			if !started {
				i := 0
				for i < len(piece) && xmlSpace(piece[i]) {
					i++
				}
				if i == len(piece) {
					continue
				}
				piece = piece[i:]
				started = true
				f.events++
				f.checksum = f.checksum*31 + 'T'
			}
			last := len(piece)
			for last > 0 && xmlSpace(piece[last-1]) {
				last--
			}
			if last == 0 {
				held = append(held, piece...)
				continue
			}
			f.checksum = foldBytes(f.checksum, held)
			f.checksum = foldBytes(f.checksum, piece[:last])
			held = append(held[:0], piece[last:]...)
		}
	}
	return result{f.events, int32(f.checksum)}, nil
}

func consumeDecoder(d *xmltext.Decoder) (result, error) {
	var f folder
	for {
		t, err := d.ReadToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result{}, err
		}
		switch t.Kind() {
		case xmltext.StartElement:
			it := t.Attrs()
			f.start([]byte(t.Name().Local), it.Len())
			for it.Next() {
				f.checksum = foldBytes(f.checksum, []byte(it.Name().Local))
				f.checksum = foldBytes(f.checksum, it.Value())
			}
		case xmltext.EndElement:
			f.end([]byte(t.Name().Local))
		case xmltext.Text, xmltext.CData:
			f.pending = append(f.pending, t.Text()...)
		}
	}
	f.flush()
	return result{f.events, int32(f.checksum)}, nil
}

func consumeLegacy(data []byte) (result, error) {
	d := xml.NewDecoder(bufio.NewReaderSize(bytes.NewReader(data), 4096))
	var f folder
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result{}, err
		}
		switch t := token.(type) {
		case xml.StartElement:
			f.start([]byte(t.Name.Local), len(t.Attr))
			for _, a := range t.Attr {
				f.checksum = foldString(f.checksum, a.Name.Local)
				f.checksum = foldString(f.checksum, a.Value)
			}
		case xml.EndElement:
			f.end([]byte(t.Name.Local))
		case xml.CharData:
			f.pending = append(f.pending, t...)
		}
	}
	f.flush()
	return result{f.events, int32(f.checksum)}, nil
}

// The v1 token types differ from legacy's; keep the same consumer without a
// per-token adapter or callback in either engine's hot path.
func consumeV1(data []byte) (result, error) {
	d := xmlv1.NewDecoder(bufio.NewReaderSize(bytes.NewReader(data), 4096))
	var f folder
	for {
		token, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result{}, err
		}
		switch t := token.(type) {
		case xmlv1.StartElement:
			f.start([]byte(t.Name.Local), len(t.Attr))
			for _, a := range t.Attr {
				f.checksum = foldString(f.checksum, a.Name.Local)
				f.checksum = foldString(f.checksum, a.Value)
			}
		case xmlv1.EndElement:
			f.end([]byte(t.Name.Local))
		case xmlv1.CharData:
			f.pending = append(f.pending, t...)
		}
	}
	f.flush()
	return result{f.events, int32(f.checksum)}, nil
}

func run() error {
	if len(os.Args) != 6 {
		return fmt.Errorf("usage: reader ENGINE FILE WARMUPS RUNS file|memory")
	}
	engine, path, mode := os.Args[1], os.Args[2], os.Args[5]
	warmups, err := strconv.Atoi(os.Args[3])
	if err != nil || warmups < 0 {
		return fmt.Errorf("invalid warmups")
	}
	runs, err := strconv.Atoi(os.Args[4])
	if err != nil || runs < 1 || (mode != "file" && mode != "memory") {
		return fmt.Errorf("invalid runs or mode")
	}
	var loaded []byte
	if mode == "memory" {
		loaded, err = os.ReadFile(path)
		if err != nil {
			return err
		}
	}
	want, err := consume(engine, path, loaded, mode)
	if err != nil {
		return err
	}
	parse := func() error {
		got, err := consume(engine, path, loaded, mode)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("non-deterministic output: got %+v, want %+v", got, want)
		}
		return nil
	}
	warmupIterations, warmupSeconds := 0, 0.0
	if warmups > 0 {
		started := time.Now()
		for warmupIterations < warmups || time.Since(started) < time.Second {
			if err := parse(); err != nil {
				return err
			}
			warmupIterations++
		}
		warmupSeconds = time.Since(started).Seconds()
	}
	values := make([]sample, 0, runs)
	for range runs {
		// Initial verification, warmup, input loading in memory mode, and JSON
		// output stay outside these process-wide allocation snapshots.
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		started, iterations := time.Now(), 0
		for iterations == 0 || time.Since(started) < 100*time.Millisecond {
			if err := parse(); err != nil {
				return err
			}
			iterations++
		}
		seconds := time.Since(started).Seconds() / float64(iterations)
		runtime.ReadMemStats(&after)
		values = append(values, sample{
			result:         want,
			Seconds:        seconds,
			Iterations:     iterations,
			Allocations:    after.Mallocs - before.Mallocs,
			AllocatedBytes: after.TotalAlloc - before.TotalAlloc,
		})
	}
	return json.NewEncoder(os.Stdout).Encode(struct {
		Engine           string   `json:"engine"`
		Version          string   `json:"version"`
		WarmupIterations int      `json:"warmupIterations"`
		WarmupSeconds    float64  `json:"warmupSeconds"`
		Samples          []sample `json:"samples"`
	}{engine, runtime.Version(), warmupIterations, warmupSeconds, values})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
