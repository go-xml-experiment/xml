// The stax-xml cross-runtime writer workload (packages/benchmark/writer-cross-runtime.mjs)
// reproduced for Go: 12,798 generated <book> records, compact output, ~10.3 MiB.
// The other runtimes run it as one cold process writing to a file, which includes JIT
// warm-up, record generation and file I/O. This program has three modes so the cost
// can be split:
//
//	cold   RECORDS OUTFILE            one run, file sink (the stax-xml harness contract)
//	steady RECORDS WARMUPS RUNS       repeated runs into memory (generation + writing)
//	gen    RECORDS WARMUPS RUNS       record generation only, nothing written
//	engine variants: add xmltext | xmltext-legacy | std as the last argument (default xmltext)
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strconv"
	"time"

	xml "github.com/go-xml-experiment/xml/internal/legacyxml"
	"github.com/go-xml-experiment/xml/internal/xmlopts"
	"github.com/go-xml-experiment/xml/xmltext"
)

const description = "Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua. Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat. Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur."

var chapterNames = [3]string{"Introduction", "Main Content", "Conclusion"}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// sink abstracts the three encoders so the generation code is shared.
type sink interface {
	start(name string, attrName, attrValue string) error
	text(s string) error
	end(name string) error
	decl() error
	close() error
}

type xmltextSink struct{ e *xmltext.Encoder }

func (s xmltextSink) start(n, an, av string) error {
	if err := s.e.BeginElement(xmltext.Name{Local: n}); err != nil {
		return err
	}
	if an != "" {
		return s.e.Attr(xmltext.Name{Local: an}, av)
	}
	return nil
}
func (s xmltextSink) text(t string) error { return s.e.Text(t) }
func (s xmltextSink) end(string) error    { return s.e.EndElement() }
func (s xmltextSink) decl() error {
	return s.e.ProcInst("xml", []byte(`version="1.0" encoding="UTF-8"`))
}
func (s xmltextSink) close() error { return s.e.Close() }

type stdSink struct{ e *xml.Encoder }

func (s stdSink) start(n, an, av string) error {
	st := xml.StartElement{Name: xml.Name{Local: n}}
	if an != "" {
		st.Attr = []xml.Attr{{Name: xml.Name{Local: an}, Value: av}}
	}
	return s.e.EncodeToken(st)
}
func (s stdSink) text(t string) error { return s.e.EncodeToken(xml.CharData(t)) }
func (s stdSink) end(n string) error {
	return s.e.EncodeToken(xml.EndElement{Name: xml.Name{Local: n}})
}
func (s stdSink) decl() error {
	return s.e.EncodeToken(xml.ProcInst{Target: "xml", Inst: []byte(`version="1.0" encoding="UTF-8"`)})
}
func (s stdSink) close() error { return s.e.Close() }

// genSink only touches the generated strings (record generation cost).
type genSink struct{ n *int }

func (s genSink) start(n, an, av string) error { *s.n += len(n) + len(an) + len(av); return nil }
func (s genSink) text(t string) error          { *s.n += len(t); return nil }
func (s genSink) end(string) error             { return nil }
func (s genSink) decl() error                  { return nil }
func (s genSink) close() error                 { return nil }

func generate(s sink, records int) error {
	if err := s.decl(); err != nil {
		return err
	}
	check := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	elem := func(name, value string) {
		check(s.start(name, "", ""))
		check(s.text(value))
		check(s.end(name))
	}
	defer func() {
		if r := recover(); r != nil {
			panic(r)
		}
	}()
	check(s.start("books", "", ""))
	for id := 1; id <= records; id++ {
		sid := strconv.Itoa(id)
		check(s.start("book", "id", "book-"+sid))
		elem("title", "Sample Book Title Number "+sid+" - Lorem ipsum dolor sit amet, consectetur adipiscing elit")
		elem("author", "Author Name "+sid)
		elem("isbn", "978"+strconv.Itoa(100000000+((id*48271)%900000000)))
		elem("publisher", "Sample Publisher "+sid)
		elem("publishDate", strconv.Itoa(2020+id%5)+"-"+pad2(id%12+1)+"-"+pad2(id%28+1))
		elem("description", description)
		check(s.start("chapters", "", ""))
		for c := 1; c <= 3; c++ {
			check(s.start("chapter", "number", strconv.Itoa(c)))
			check(s.text(chapterNames[c-1] + " Chapter for Book " + sid))
			check(s.end("chapter"))
		}
		check(s.end("chapters"))
		check(s.end("book"))
	}
	check(s.end("books"))
	return s.close()
}

func newSink(engine string, w io.Writer) sink {
	switch engine {
	case "xmltext":
		return xmltextSink{xmltext.NewEncoder(w)}
	case "xmltext-legacy":
		return xmltextSink{xmltext.NewEncoder(w, xmlopts.LegacyOutput(true))}
	case "std":
		return stdSink{xml.NewEncoder(w)}
	}
	panic("unknown engine " + engine)
}

func run() (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	if len(os.Args) < 4 {
		return fmt.Errorf("usage: see source")
	}
	mode := os.Args[1]
	records, _ := strconv.Atoi(os.Args[2])
	engine := "xmltext"
	if mode == "cold" {
		if len(os.Args) > 4 {
			engine = os.Args[4]
		}
		started := time.Now()
		f, err := os.Create(os.Args[3])
		if err != nil {
			return err
		}
		bw := bufio.NewWriterSize(f, 64<<10)
		if err := generate(newSink(engine, bw), records); err != nil {
			return err
		}
		if err := bw.Flush(); err != nil {
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		sec := time.Since(started).Seconds()
		st, _ := os.Stat(os.Args[3])
		fmt.Printf("{\"records\":%d,\"bytes\":%d,\"seconds\":%.9f,\"throughputMiBs\":%.6f}\n", records, st.Size(), sec, float64(st.Size())/1048576/sec)
		return nil
	}
	warm, _ := strconv.Atoi(os.Args[3])
	runs, _ := strconv.Atoi(os.Args[4])
	if len(os.Args) > 5 {
		engine = os.Args[5]
	}
	var buf bytes.Buffer
	buf.Grow(12 << 20)
	var n int
	once := func() error {
		buf.Reset()
		if mode == "gen" {
			n = 0
			return generate(genSink{&n}, records)
		}
		return generate(newSink(engine, &buf), records)
	}
	if err := once(); err != nil {
		return err
	}
	size := buf.Len()
	if mode == "gen" {
		size = n
	}
	t0 := time.Now()
	for i := 0; i < warm || time.Since(t0) < time.Second; i++ {
		if err := once(); err != nil {
			return err
		}
	}
	var secs []float64
	for range runs {
		t, k := time.Now(), 0
		for k == 0 || time.Since(t) < 200*time.Millisecond {
			if err := once(); err != nil {
				return err
			}
			k++
		}
		secs = append(secs, time.Since(t).Seconds()/float64(k))
	}
	return json.NewEncoder(os.Stdout).Encode(map[string]any{"engine": engine, "mode": mode, "records": records, "bytes": size, "version": runtime.Version(), "seconds": secs})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
