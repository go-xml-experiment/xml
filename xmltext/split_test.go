// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"reflect"
	"strings"
	"testing"
)

// rec is what a Decoder user sees of one token.
type rec struct {
	kind    Kind
	s       string // name and attributes for tags, text otherwise
	partial bool
}

func (r rec) String() string {
	return fmt.Sprintf("%v %q partial=%v", r.kind, r.s, r.partial)
}

// record reads every token. A nil error means the input ended with io.EOF.
func record(d *Decoder) ([]rec, error) {
	var out []rec
	for {
		tk, err := d.ReadToken()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		r := rec{kind: tk.Kind(), partial: tk.Partial()}
		switch r.kind {
		case StartElement:
			r.s = fmt.Sprint(tk.Name())
			for it := tk.Attrs(); it.Next(); {
				r.s += fmt.Sprintf(" %v=%q", it.Name(), it.Value())
			}
		case EndElement:
			r.s = fmt.Sprint(tk.Name())
		default:
			r.s = string(tk.Text())
		}
		out = append(out, r)
	}
}

// mergePieces joins the pieces of each Text run and CDATA section.
func mergePieces(in []rec) []rec {
	var out []rec
	for i := 0; i < len(in); i++ {
		r := in[i]
		for r.partial && i+1 < len(in) && in[i+1].kind == r.kind {
			i++
			r.s += in[i].s
			r.partial = in[i].partial
		}
		out = append(out, r)
	}
	return out
}

// randChunkReader returns random-sized chunks (1 to max bytes).
type randChunkReader struct {
	r   io.Reader
	rng *rand.Rand
	max int
}

func (r *randChunkReader) Read(p []byte) (int, error) {
	return r.r.Read(p[:min(len(p), 1+r.rng.Intn(r.max))])
}

const inputForms = 5

// newDec opens in in one of inputForms forms: bytes, Reader, one-byte Reader,
// 7-byte chunks, random chunks. With SplitText, window > 0 replaces the 32 KiB
// piece size (test hook, not an option).
func newDec(in string, form, window int, opts ...Options) *Decoder {
	var d *Decoder
	switch form {
	case 0:
		d = NewDecoderBytes([]byte(in), opts...)
	case 1:
		d = NewDecoder(strings.NewReader(in), opts...)
	case 2:
		d = NewDecoder(&charsetChunkReader{r: strings.NewReader(in), n: 1}, opts...)
	case 3:
		d = NewDecoder(&charsetChunkReader{r: strings.NewReader(in), n: 7}, opts...)
	default:
		d = NewDecoder(&randChunkReader{r: strings.NewReader(in), rng: rand.New(rand.NewSource(int64(len(in)))), max: 40}, opts...)
	}
	if window > 0 {
		d.eng.textLimit = window
	}
	return d
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// checkWhole decodes in in every form, whole nodes, and checks that the
// token sequences are identical and all forms fail or none does (the wording
// of a malformed start tag's error can depend on how much input has arrived,
// as for "<\">"), no Text is adjacent to another
// Text and nothing is Partial. It returns the sequence.
func checkWhole(t testing.TB, in string, opts ...Options) ([]rec, error) {
	t.Helper()
	want, werr := record(newDec(in, 0, 0, opts...))
	for i, r := range want {
		if r.partial {
			t.Fatalf("input %.60q: whole mode token %d is Partial", in, i)
		}
		if i > 0 && r.kind == Text && want[i-1].kind == Text {
			t.Fatalf("input %.60q: adjacent Text tokens %d and %d", in, i-1, i)
		}
	}
	for form := 1; form < inputForms; form++ {
		got, err := record(newDec(in, form, 0, opts...))
		if (err == nil) != (werr == nil) || !reflect.DeepEqual(got, want) {
			t.Fatalf("input %.60q: form %d differs from bytes input\n  got %v %v\n want %v %v", in, form, got, err, want, werr)
		}
	}
	return want, werr
}

func wholeEdgeInputs() []string {
	long := strings.Repeat("한😀x\r\n&amp;&#65;]]x", 9000) // ~190 KiB, entity heavy
	ents := strings.Repeat("&lt;&#x41;&amp;", 20000)
	return []string{
		"<r>" + long + "</r>",
		"<r>" + ents + "</r>",
		"<r><![CDATA[" + strings.Repeat("한😀x\r\n]]", 20000) + "]]></r>",
		"<r><![CDATA[" + long + "]]><![CDATA[" + ents + "]]>t" + long + "<![CDATA[]]></r>",
		"<r>" + strings.Repeat("x", 100000) + "&" + strings.Repeat("n", 40000) + ";y</r>",
		"<r>" + strings.Repeat("x", 100000) + "]]></r>",
		"<r>" + strings.Repeat("x", 70000),
	}
}

func TestWholeTextSameForEveryInput(t *testing.T) {
	for i, in := range wholeEdgeInputs() {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			opts := []Options{WithEntities(map[string]string{strings.Repeat("n", 40000): "N"})}
			got, err := checkWhole(t, in, opts...)
			// A text run or CDATA section is one token however long.
			texts := 0
			for _, r := range got {
				if r.kind == Text || r.kind == CData {
					texts++
				}
			}
			if err == nil && texts == 0 {
				t.Fatal("no text")
			}
			if i == 0 && (len(got) != 3 || len(got[1].s) < 100000) {
				t.Fatalf("long text is %d tokens", len(got))
			}
		})
	}
}

// Random documents of text, references, CDATA and tags, read through readers
// that return random chunks, give the bytes input's tokens.
func TestWholeTextRandomSplits(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	frags := []string{"x", "yy", "한", "😀", "\r\n", "\r", "\n", "&amp;", "&#65;", "&#x1F600;", "&bogus;", "&", "]", "]]", "]]>",
		"<![CDATA[", "]]>", "<![CDATA[a]]>", "<b/>", "<c>t</c>", "<!--c-->", "<?p i?>", "\xff", "&#0;"}
	for n := 0; n < 3000; n++ {
		var b strings.Builder
		b.WriteString("<r>")
		for k := rng.Intn(30); k >= 0; k-- {
			b.WriteString(frags[rng.Intn(len(frags))])
			if rng.Intn(5) == 0 {
				b.WriteString(strings.Repeat("z", rng.Intn(70000)))
			}
		}
		b.WriteString("</r>")
		checkWhole(t, b.String(), AllowUnknownEntities(rng.Intn(2) == 0))
	}
}

func FuzzWholeInputForms(f *testing.F) {
	for _, s := range roundTripSeeds {
		f.Add(s)
	}
	f.Add("<r>" + strings.Repeat("a&amp;b", 5000) + "<![CDATA[" + strings.Repeat("]", 70000) + "]]></r>")
	f.Fuzz(func(t *testing.T, in string) { checkWhole(t, in) })
}

func repeatDoc(head, tail string, n int) io.Reader {
	return io.MultiReader(strings.NewReader(head), io.LimitReader(repeatReader{'x'}, int64(n)), strings.NewReader(tail))
}

func TestDefaultMaxTokenSize(t *testing.T) {
	const limit = 16 << 20
	// dec opens head + n bytes + tail as bytes or Reader input.
	dec := func(form int, head, tail string, n int, opts ...Options) *Decoder {
		r := repeatDoc(head, tail, n)
		if form == 0 {
			b, _ := io.ReadAll(r)
			return NewDecoderBytes(b, opts...)
		}
		return NewDecoder(r, opts...)
	}
	for form := 0; form < 2; form++ {
		for _, c := range []struct {
			name, head, tail string
			n                int
			fail             bool
		}{
			{"text at the limit", "<r>", "</r>", limit, false},
			{"text over the limit", "<r>", "</r>", limit + 1, true},
			{"cdata at the limit", "<r><![CDATA[", "]]></r>", limit - 12, false},
			{"cdata over the limit", "<r><![CDATA[", "]]></r>", limit - 11, true},
		} {
			err := drainAll(dec(form, c.head, c.tail, c.n))
			if c.fail != (err != nil) || err != nil && (!strings.Contains(err.Error(), "MaxTokenSize") || !strings.Contains(err.Error(), "SplitText")) {
				t.Errorf("form %d %s: got %v", form, c.name, err)
			}
			if !c.fail {
				continue
			}
			for _, o := range []Options{MaxTokenSize(0), MaxTokenSize(-1), SplitText(true), MaxTokenSize(limit + 12)} {
				if err := drainAll(dec(form, c.head, c.tail, c.n, o)); err != nil {
					t.Errorf("form %d %s with %+v: %v", form, c.name, o, err)
				}
			}
		}
	}
	// an explicit limit replaces the default
	if err := drainAll(NewDecoderBytes([]byte("<r>"+strings.Repeat("x", 100)+"</r>"), MaxTokenSize(50))); err == nil || !strings.Contains(err.Error(), "SplitText") {
		t.Errorf("MaxTokenSize(50): %v", err)
	}
	// other tokens keep the plain message
	if err := drainAll(NewDecoderBytes([]byte("<r><!--"+strings.Repeat("x", 100)+"--></r>"), MaxTokenSize(50))); err == nil || strings.Contains(err.Error(), "SplitText") {
		t.Errorf("comment over MaxTokenSize(50): %v", err)
	}
}

func TestMaxAttributes(t *testing.T) {
	tag := func(n int) string { return manyAttrs(n) + "/>" }
	decl := func(n, decls int) string {
		var b strings.Builder
		b.WriteString(manyAttrs(n))
		for i := 0; i < decls; i++ {
			fmt.Fprintf(&b, ` xmlns:p%d="u"`, i)
		}
		b.WriteString(` xmlns="d"/>`)
		return b.String()
	}
	for _, c := range []struct {
		name, in string
		opts     []Options
		fail     bool
	}{
		{"default 1000", tag(1000), nil, false},
		{"default 1001", tag(1001), nil, true},
		{"default counts declarations", decl(999, 1), nil, true}, // 999 + xmlns:p0 + xmlns = 1001
		{"default declarations at limit", decl(998, 1), nil, false},
		{"limit 3", tag(3), []Options{MaxAttributes(3)}, false},
		{"limit 3 over", tag(4), []Options{MaxAttributes(3)}, true},
		{"limit raised", tag(1500), []Options{MaxAttributes(2000)}, false},
		{"zero unlimited", tag(5000), []Options{MaxAttributes(0)}, false},
		{"negative unlimited", tag(5000), []Options{MaxAttributes(-1)}, false},
		{"permissive", tag(1001), []Options{Permissive(true)}, true},
	} {
		for _, form := range []int{0, 1} {
			err := drainAll(newDec(c.in, form, 0, c.opts...))
			if (err != nil) != c.fail {
				t.Errorf("%s form %d: err = %v, want failure %v", c.name, form, err, c.fail)
			}
			if err != nil && !strings.Contains(err.Error(), "MaxAttributes") {
				t.Errorf("%s: error does not name MaxAttributes: %v", c.name, err)
			}
		}
	}
}

// pieceSweepInputs puts each edge in text and in CDATA at every offset around
// a window boundary.
func pieceSweepInputs(window int) []string {
	var out []string
	textEdges := []string{"&amp;", "&#x1F600;", "&#65;", "한", "😀", "\r\n", "\r", "]", "]]", "]]x", "&bogus;", "&", "x"}
	cdEdges := []string{"한", "😀", "\r\n", "\r", "]", "]]", "]]]", "]]x", "]>", ">", "&amp;", "x"}
	for p := 0; p < window+6; p++ {
		pre, post := strings.Repeat("x", p), strings.Repeat("y", 3)
		for _, e := range textEdges {
			out = append(out, "<r>"+pre+e+post+"</r>", "<r>"+pre+e+e+"</r>")
		}
		for _, e := range cdEdges {
			out = append(out,
				"<r><![CDATA["+pre+e+post+"]]></r>",
				"<r><![CDATA["+pre+e+"]]><![CDATA["+e+post+"]]></r>",
				"<r>"+pre+"<![CDATA["+e+e+"]]>"+e+"</r>",
				"<r><![CDATA["+pre+e+"]]]]><![CDATA[>]]></r>")
		}
		out = append(out, "<r><![CDATA["+pre+"]]><![CDATA["+pre+"]]><![CDATA[]]></r>")
	}
	return out
}

// checkSplit: the pieces of in, with the given window, merge into the whole
// tokens, in every input form; Partial is consistent; nothing else changes.
func checkSplit(t testing.TB, in string, window int, opts ...Options) {
	t.Helper()
	whole, werr := record(newDec(in, 0, 0, opts...))
	so := append([]Options{SplitText(true)}, opts...)
	first, ferr := record(newDec(in, 0, window, so...))
	if (ferr == nil) != (werr == nil) {
		t.Fatalf("input %.80q window %d: split err %v, whole err %v", in, window, ferr, werr)
	}
	for i, r := range first {
		if r.partial {
			// a decode error may end the stream after a Partial piece
			if r.kind != Text && r.kind != CData || i+1 < len(first) && first[i+1].kind != r.kind || i+1 == len(first) && ferr == nil {
				t.Fatalf("input %.80q window %d: Partial %v not followed by the same kind: %v", in, window, r, first)
			}
		}
	}
	if werr == nil {
		if got := mergePieces(first); !reflect.DeepEqual(got, whole) {
			t.Fatalf("input %.80q window %d: pieces merge to\n %v\nwant\n %v\npieces %v", in, window, got, whole, first)
		}
	}
	for form := 1; form < inputForms; form++ {
		got, err := record(newDec(in, form, window, so...))
		if (err == nil) != (ferr == nil) || !reflect.DeepEqual(got, first) {
			t.Fatalf("input %.80q window %d: form %d differs from bytes input\n got %v %v\nwant %v %v", in, window, form, got, err, first, ferr)
		}
	}
}

func TestSplitTextPieces(t *testing.T) {
	// Permissive reports truncated CDATA with its own message; Loose with
	// AutoClose can synthesize an EndElement between tokens.
	for _, opts := range [][]Options{nil, {Permissive(true)}, {Loose(true), AutoClose([]string{"b", "c"})}} {
		for _, w := range []int{16, 17, 20, 31} {
			for _, in := range pieceSweepInputs(w) {
				checkSplit(t, in, w, opts...)
			}
			for p := 0; p < 40; p++ {
				pre := strings.Repeat("x", p)
				checkSplit(t, "<r><b>"+pre+"<![CDATA["+pre+"]]>"+pre+"<c>"+pre+"</c>"+pre+"</r>", w, opts...)
			}
		}
	}
}

func TestSplitTextPartial(t *testing.T) {
	// A text run of 100 bytes in pieces of at most 16: every piece but the last is Partial.
	in := "<r>" + strings.Repeat("x", 100) + "<b/>" + strings.Repeat("y", 40) + "</r>"
	got, err := record(newDec(in, 0, 16, SplitText(true)))
	if err != nil {
		t.Fatal(err)
	}
	var texts []rec
	for _, r := range got {
		if r.kind == Text {
			texts = append(texts, r)
		}
	}
	for i, r := range texts {
		last := i == 6 || i == len(texts)-1 // the run before <b/> and the final run
		if (r.partial) == last || len(r.s) > 16 {
			t.Fatalf("piece %d %v", i, r)
		}
	}
	// Adjacent CDATA sections are two runs of pieces; the first one's last piece is not Partial.
	in = "<r><![CDATA[" + strings.Repeat("a", 30) + "]]><![CDATA[" + strings.Repeat("b", 30) + "]]></r>"
	got, _ = record(newDec(in, 0, 16, SplitText(true)))
	var ends int
	var sb strings.Builder
	for _, r := range got {
		if r.kind != CData {
			continue
		}
		sb.WriteString(r.s)
		if !r.partial {
			ends++
			sb.WriteByte('|')
		}
	}
	if ends != 2 || sb.String() != strings.Repeat("a", 30)+"|"+strings.Repeat("b", 30)+"|" {
		t.Fatalf("sections %d: %q", ends, sb.String())
	}
	// the default window is 32 KiB for Reader and bytes input
	big := "<r>" + strings.Repeat("x", 100<<10) + "<![CDATA[" + strings.Repeat("y", 100<<10) + "]]></r>"
	for form := 0; form < 2; form++ {
		got, err := record(newDec(big, form, 0, SplitText(true)))
		if err != nil {
			t.Fatal(err)
		}
		var sizes []int
		for _, r := range got {
			if r.kind == Text || r.kind == CData {
				sizes = append(sizes, len(r.s))
			}
		}
		if len(sizes) != 8 || sizes[0] > 32<<10 || sizes[4] > 32<<10-9 {
			t.Fatalf("form %d: piece sizes %v", form, sizes)
		}
	}
	// With MaxTokenSize smaller than the window it sets the piece size.
	got, err = record(NewDecoderBytes([]byte("<r>"+strings.Repeat("x", 100)+"</r>"), SplitText(true), MaxTokenSize(30)))
	if err != nil || len(mergePieces(got)) != 3 || len(got[1].s) > 30 || !got[1].partial {
		t.Fatalf("MaxTokenSize(30): %v %v", got, err)
	}
}

// Whatever the limit, SplitText either splits correctly or reports the limit.
func TestSplitTextSmallLimits(t *testing.T) {
	inputs := []string{
		"<r>" + strings.Repeat("한x&amp;\r\n", 12) + "</r>",
		"<r><![CDATA[" + strings.Repeat("한x]\r\n", 12) + "]]></r>",
		"<r>t<![CDATA[]]>t</r>",
	}
	for _, in := range inputs {
		whole, _ := record(newDec(in, 0, 0))
		for limit := 1; limit <= 24; limit++ {
			for form := 0; form < inputForms; form++ {
				got, err := record(newDec(in, form, 0, SplitText(true), MaxTokenSize(limit)))
				if err != nil {
					if !strings.Contains(err.Error(), "MaxTokenSize") {
						t.Fatalf("limit %d form %d: %v", limit, form, err)
					}
					continue
				}
				if m := mergePieces(got); !reflect.DeepEqual(m, whole) {
					t.Fatalf("limit %d form %d: %v, want %v", limit, form, m, whole)
				}
			}
		}
	}
}

func FuzzSplitText(f *testing.F) {
	for _, s := range roundTripSeeds {
		f.Add(s, uint8(0))
	}
	for _, s := range pieceSweepInputs(16)[:200] {
		f.Add(s, uint8(3))
	}
	f.Fuzz(func(t *testing.T, in string, w uint8) {
		checkSplit(t, in, 16+int(w)%32)
	})
}

// A 64 MiB CDATA section and a 64 MiB text run stream through a Reader in
// bounded memory.
func TestSplitTextBoundedMemory(t *testing.T) {
	const size = 64 << 20
	for _, node := range []struct{ name, head, tail string }{{"cdata", "<r><![CDATA[", "]]></r>"}, {"text", "<r>", "</r>"}} {
		t.Run(node.name, func(t *testing.T) {
			d := NewDecoder(repeatDoc(node.head, node.tail, size), SplitText(true))
			total, maxCap, maxPiece, pieces := 0, 0, 0, 0
			for {
				tk, err := d.ReadToken()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if k := tk.Kind(); k == Text || k == CData {
					total += len(tk.Text())
					maxPiece = max(maxPiece, len(tk.Text()))
					pieces++
				}
				maxCap = max(maxCap, cap(d.buf))
			}
			if total != size {
				t.Fatalf("read %d bytes, want %d", total, size)
			}
			if maxCap > 256<<10 || maxPiece > textWindow {
				t.Fatalf("buffer capacity %d, largest piece %d", maxCap, maxPiece)
			}
		})
	}
}

// Whole text on a Reader buffers the node, bounded by MaxTokenSize.
func TestWholeTextBufferBoundedByLimit(t *testing.T) {
	d := NewDecoder(repeatDoc("<r>", "</r>", 3<<20), MaxTokenSize(1<<20))
	err := drainAll(d)
	if err == nil || !strings.Contains(err.Error(), "SplitText") {
		t.Fatalf("got %v", err)
	}
	if cap(d.buf) > 8<<20 {
		t.Fatalf("buffer grew to %d bytes with a 1 MiB limit", cap(d.buf))
	}
}

func TestPartialOfClonedAndOtherKinds(t *testing.T) {
	d := NewDecoderBytes([]byte("<r><![CDATA["+strings.Repeat("a", 40)+"]]></r>"), SplitText(true))
	d.eng.textLimit = 16
	var sawPartial bool
	for {
		tk, err := d.ReadToken()
		if err != nil {
			break
		}
		c := tk.Clone()
		if c.Partial() != tk.Partial() {
			t.Fatal("Clone loses Partial")
		}
		if tk.Kind() != CData && tk.Partial() {
			t.Fatalf("%v is Partial", tk.Kind())
		}
		sawPartial = sawPartial || tk.Partial()
	}
	if !sawPartial {
		t.Fatal("no Partial piece")
	}
}

// ---- Encoder ----

func encodeCData(t *testing.T, pieces ...string) string {
	t.Helper()
	var out bytes.Buffer
	e := NewEncoder(&out)
	e.BeginElement(Name{Local: "r"})
	for i, p := range pieces {
		if err := e.cdata([]byte(p), i < len(pieces)-1); err != nil {
			t.Fatal(err)
		}
	}
	e.EndElement()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

func TestEncoderPartialCData(t *testing.T) {
	for _, pieces := range [][]string{
		{"ab", "cd"},
		{"x]]", ">y"},
		{"x]", "]>y"},
		{"]", "]", ">"},
		{"]]", "]>"},
		{"]]", "]]>"},
		{"]", "]]>"},
		{"a", "", "b"},
		{"]", "", "]", "", ">"},
		{"x", "]]>", "]]", ">"},
		{"", "a"},
	} {
		out := encodeCData(t, pieces...)
		want := strings.Join(pieces, "")
		got, err := record(NewDecoderBytes([]byte(out)))
		if err != nil {
			t.Errorf("%q: output %q does not parse: %v", pieces, out, err)
			continue
		}
		var text string
		for _, r := range got {
			if r.kind == CData {
				text += r.s
			}
		}
		if text != want {
			t.Errorf("%q: output %q reads back %q, want %q", pieces, out, text, want)
		}
		if c := strings.Count(out, "<![CDATA["); !strings.Contains(strings.Join(pieces, "|"), "]") && c != 1 {
			t.Errorf("%q: %d sections in %q, want 1", pieces, c, out)
		}
	}
	// the section stays open across pieces and is closed by anything else
	if got := encodeCData(t, "ab", "cd"); got != "<r><![CDATA[abcd]]></r>" {
		t.Errorf("got %q", got)
	}
	var out bytes.Buffer
	e := NewEncoder(&out)
	e.BeginElement(Name{Local: "r"})
	e.cdata([]byte("a"), true)
	e.Text("t")
	e.cdata([]byte("b"), true)
	e.Comment([]byte("c"))
	e.cdata([]byte("d"), true)
	e.BeginElement(Name{Local: "x"})
	e.EndElement()
	e.cdata([]byte("e"), true)
	e.EndElement()
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	if want := "<r><![CDATA[a]]>t<![CDATA[b]]><!--c--><![CDATA[d]]><x/><![CDATA[e]]></r>"; out.String() != want {
		t.Errorf("got %q\nwant %q", out.String(), want)
	}
	// an unfinished section is closed by Close
	out.Reset()
	e = NewEncoder(&out, AllowFragments(true))
	e.cdata([]byte("a"), true)
	if err := e.Close(); err != nil || out.String() != "<![CDATA[a]]>" {
		t.Errorf("Close: %q %v", out.String(), err)
	}
}

// Decoding in pieces and writing the tokens keeps the section boundaries.
func TestSplitRoundTripKeepsSections(t *testing.T) {
	for _, w := range []int{16, 19, 32} {
		for _, in := range pieceSweepInputs(w) {
			checkRoundTrip(t, []byte(in), w)
		}
	}
}
