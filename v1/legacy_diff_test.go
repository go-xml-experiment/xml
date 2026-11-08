package xml

import (
	"bytes"
	stdxml "encoding/xml"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	legacy "github.com/go-xml-experiment/xml/internal/legacyxml"
)

// The frozen encoding/xml in internal/legacyxml is the oracle. These tests
// check that this package, which runs on xmltext, produces the same tokens,
// offsets and errors on the same input, token by token.

var tokenCfgs = []tokenCfg{
	{name: "strict", strict: true},
	{name: "strict-raw", strict: true, raw: true},
	{name: "loose"},
	{name: "html", autoClose: HTMLAutoClose, entity: HTMLEntity},
}

func legacyAttrsOf(as []legacy.Attr) string {
	var sb strings.Builder
	for _, a := range as {
		fmt.Fprintf(&sb, "{%q %q %q}", a.Name.Space, a.Name.Local, a.Value)
	}
	return sb.String()
}

// renderLegacy renders a legacy token in the form renderNew uses.
func renderLegacy(t legacy.Token) string {
	switch v := t.(type) {
	case legacy.StartElement:
		return fmt.Sprintf("S %q %q %q", v.Name.Space, v.Name.Local, legacyAttrsOf(v.Attr))
	case legacy.EndElement:
		return fmt.Sprintf("E %q %q", v.Name.Space, v.Name.Local)
	case legacy.CharData:
		return fmt.Sprintf("T %q", []byte(v))
	case legacy.Comment:
		return fmt.Sprintf("C %q", []byte(v))
	case legacy.ProcInst:
		return fmt.Sprintf("P %q %q", v.Target, v.Inst)
	case legacy.Directive:
		return fmt.Sprintf("D %q", []byte(v))
	}
	return fmt.Sprintf("? %T", t)
}

// runLegacy returns the rendered tokens (with the input offset after
// each one) and the final error.
func runLegacy(in string, c tokenCfg) (out []string, err error) {
	d := legacy.NewDecoder(strings.NewReader(in))
	d.Strict, d.AutoClose, d.Entity = c.strict, c.autoClose, c.entity
	for len(out) < 5000 {
		var t legacy.Token
		if c.raw {
			t, err = d.RawToken()
		} else {
			t, err = d.Token()
		}
		if err != nil {
			return out, err
		}
		l, col := d.InputPos()
		out = append(out, fmt.Sprintf("%s @%d:%d:%d", renderLegacy(t), d.InputOffset(), l, col))
	}
	return out, nil
}

// compareLegacyTokens reports a description of the first difference, ignoring the
// position of a token that was cut short by an error.
func compareLegacyTokens(in string, c tokenCfg, positions bool) string {
	want, werr := runLegacy(in, c)
	got, gerr := runNew(in, c)
	strip := func(ss []string) []string {
		if positions {
			return ss
		}
		out := make([]string, len(ss))
		for i, s := range ss {
			out[i] = s[:strings.LastIndex(s, " @")]
		}
		return out
	}
	w, g := strip(want), strip(got)
	if werr != nil && gerr != nil {
		// both fail: tokens before the failure agree and the error text matches
		n := min(len(w), len(g))
		if !reflect.DeepEqual(w[:n], g[:n]) {
			return fmt.Sprintf("tokens differ before the error\n legacy %q (%v)\n    new %q (%v)", w, werr, g, gerr)
		}
		if len(w) != len(g) && !(c.raw) {
			return fmt.Sprintf("different number of tokens before the error: legacy %d (%v), new %d (%v)\n legacy %q\n    new %q", len(w), werr, len(g), gerr, w, g)
		}
		if errString(werr) != errString(gerr) {
			return fmt.Sprintf("error differs: legacy %s, new %s", errString(werr), errString(gerr))
		}
		return ""
	}
	if (werr == nil) != (gerr == nil) || !reflect.DeepEqual(w, g) {
		return fmt.Sprintf("legacy %q err=%v\n    new %q err=%v", w, werr, g, gerr)
	}
	return ""
}

func legacyTestInputs(t testing.TB) []string {
	files, err := filepath.Glob("../internal/legacyxml/*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no legacy test files: %v", err)
	}
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if strings.Contains(s, "<") && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	var fold func(e ast.Expr) (string, bool)
	fold = func(e ast.Expr) (string, bool) {
		switch v := e.(type) {
		case *ast.BasicLit:
			if v.Kind != token.STRING {
				return "", false
			}
			s, err := strconv.Unquote(v.Value)
			return s, err == nil
		case *ast.BinaryExpr:
			if v.Op != token.ADD {
				return "", false
			}
			l, ok1 := fold(v.X)
			r, ok2 := fold(v.Y)
			return l + r, ok1 && ok2
		case *ast.ParenExpr:
			return fold(v.X)
		}
		return "", false
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), f, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if e, ok := n.(ast.Expr); ok {
				if s, ok := fold(e); ok {
					add(s)
					if _, bin := e.(*ast.BinaryExpr); bin {
						return false
					}
				}
			}
			return true
		})
	}
	return out
}

func TestTokensAgainstLegacy(t *testing.T) {
	inputs := legacyTestInputs(t)
	bad := 0
	for _, c := range tokenCfgs {
		for _, in := range inputs {
			if d := compareLegacyTokens(in, c, true); d != "" {
				bad++
				if bad <= 25 {
					t.Errorf("[%s] %q\n%s", c.name, in, d)
				}
			}
		}
	}
	t.Logf("%d inputs x %d configurations, %d differences", len(inputs), len(tokenCfgs), bad)
}

func FuzzTokensAgainstLegacy(f *testing.F) {
	for _, in := range legacyTestInputs(f) {
		f.Add(in, byte(0))
		f.Add(in, byte(1))
		f.Add(in, byte(2))
		f.Add(in, byte(3))
	}
	f.Fuzz(func(t *testing.T, in string, mode byte) {
		c := tokenCfgs[int(mode)%len(tokenCfgs)]
		if d := compareAcceptance(in, c); d != "" {
			t.Fatalf("[%s] %q\n%s", c.name, in, d)
		}
	})
}

// TestRandomAgainstLegacy tokenizes random strings over a markup-heavy
// alphabet with both implementations and lists the kinds of difference found.
func TestRandomAgainstLegacy(t *testing.T) {
	n := 60000
	if testing.Short() {
		n = 5000
	}
	alphabet := []string{"<", ">", "/", "!", "-", "--", "[", "]", "?", "=", "\"", "'", " ", "&", ";", "a", "x:y", ":", "\n", "#", "0", "<a>", "</a>", "<![CDATA[", "]]>", "<!--", "-->", "<?", "?>", "&amp;", "&#65;", "xml", "é"}
	rng := newTestRand(7)
	kinds := map[string]int{}
	example := map[string]string{}
	total := 0
	for i := 0; i < n; i++ {
		var sb strings.Builder
		for j, k := 0, 1+rng.Intn(9); j < k; j++ {
			sb.WriteString(alphabet[rng.Intn(len(alphabet))])
		}
		in := sb.String()
		for _, c := range tokenCfgs {
			if d := compareLegacyTokens(in, c, true); d != "" {
				total++
				key := c.name + ": " + strings.SplitN(d, "\n", 2)[0]
				key = regexpNum.ReplaceAllString(key, "N")
				kinds[key]++
				if _, ok := example[key]; !ok {
					example[key] = fmt.Sprintf("%q", in)
				}
			}
		}
	}
	for k, v := range kinds {
		t.Logf("%5d  %s   e.g. %s", v, k, example[k])
	}
	if total > 0 {
		t.Errorf("%d differences in %d x %d runs", total, n, len(tokenCfgs))
	}
}

var regexpNum = regexp.MustCompile(`[0-9]+`)

func newTestRand(seed int64) *rand.Rand { return rand.New(rand.NewSource(seed)) }

// compareAcceptance is the strict requirement for arbitrary input:
// the tokens returned before any error, and whether the input is accepted,
// are identical. (When one input holds several errors the one reported can
// differ, because encoding/xml checks text after reading it and xmltext as it
// goes; that is only checked on the curated and the seeded random inputs.)
func compareAcceptance(in string, c tokenCfg) string {
	want, werr := runLegacy(in, c)
	got, gerr := runNew(in, c)
	if (werr == nil || werr == io.EOF) != (gerr == nil || gerr == io.EOF) {
		return fmt.Sprintf("accept/reject differs: legacy %q err=%v\n    new %q err=%v", want, werr, got, gerr)
	}
	n := min(len(want), len(got))
	if werr == nil || werr == io.EOF {
		n = len(want)
		if len(got) != len(want) {
			return fmt.Sprintf("token count differs: legacy %q\n    new %q", want, got)
		}
	}
	// compare token text only; positions after a token that was cut short by an error may differ
	strip := func(s string) string { return s[:strings.LastIndex(s, " @")] }
	for i := 0; i < n; i++ {
		if strip(want[i]) != strip(got[i]) && !(i == n-1 && werr != nil && werr != io.EOF) {
			return fmt.Sprintf("token %d differs: legacy %q new %q", i, want[i], got[i])
		}
	}
	return ""
}

// legacyPositions is positions with internal/legacyxml as the decoder.
func legacyPositions(in string, rk readerKind) (out []string, err error) {
	r, rem := rk.make(in)
	d := legacy.NewDecoder(r)
	for len(out) < 2000 {
		t, e := d.Token()
		if e != nil {
			out = append(out, fmt.Sprintf("err left=%d", rem()))
			return out, e
		}
		out = append(out, fmt.Sprintf("%s left=%d", renderLegacy(t), rem()))
	}
	return out, nil
}

func TestReaderPositionAgainstLegacy(t *testing.T) {
	inputs := legacyTestInputs(t)
	inputs = append(inputs,
		"<a>"+strings.Repeat("x", 10000)+"</a>trailing",
		"<a>"+strings.Repeat("<!-- c -->", 600)+"</a>",
		"<a "+strings.Repeat(`k="v" `, 2000)+"/>rest",
		"<a><![CDATA["+strings.Repeat("y", 9000)+"]]></a>",
		"<a>"+strings.Repeat("é", 5000)+"</a>\n",
	)
	bad := 0
	for _, rk := range readerKinds {
		for _, in := range inputs {
			want, werr := legacyPositions(in, rk)
			got, gerr := positions(in, rk)
			// positions after a failing token are not meaningful when the error differs
			if len(want) != len(got) || (werr == nil) != (gerr == nil) {
				bad++
				if bad <= 15 {
					t.Errorf("[%s] %.60q: %d vs %d tokens (%v / %v)", rk.name, in, len(want), len(got), werr, gerr)
				}
				continue
			}
			for i := range want {
				if want[i] != got[i] && !strings.HasPrefix(want[i], "err") {
					bad++
					if bad <= 15 {
						t.Errorf("[%s] %.60q token %d:\n legacy %q\n    new %q", rk.name, in, i, want[i], got[i])
					}
					break
				}
			}
		}
	}
	t.Logf("%d inputs x %d reader kinds, %d differences", len(inputs), len(readerKinds), bad)
}

// With an entity map the settings are consulted at every Read, and the
// positions must still match.
func TestBufferSourceEntityPositionAgainstLegacy(t *testing.T) {
	input := "<r>" + strings.Repeat(`<i a="&amp;&lt;">x &amp; y &nbsp; z</i>`, 200) + "tail</r>rest"
	rk := bufferKind(0)
	run := func(newImpl bool) (out []string) {
		r, rem := rk.make(input)
		var next func() (string, error)
		if newImpl {
			d := NewDecoder(r)
			d.Strict, d.Entity = false, HTMLEntity
			next = func() (string, error) { t, e := d.Token(); return renderNew(t), e }
		} else {
			d := legacy.NewDecoder(r)
			d.Strict, d.Entity = false, legacy.HTMLEntity
			next = func() (string, error) { t, e := d.Token(); return renderLegacy(t), e }
		}
		for {
			s, err := next()
			out = append(out, fmt.Sprintf("%s err=%v left=%d", s, err, rem()))
			if err != nil {
				return
			}
		}
	}
	if want, got := run(false), run(true); !reflect.DeepEqual(got, want) {
		for i := range want {
			if i >= len(got) || got[i] != want[i] {
				t.Fatalf("token %d:\n legacy %q\n    new %q", i, want[i], got[min(i, len(got)-1)])
			}
		}
		t.Fatalf("token counts differ: %d vs %d", len(want), len(got))
	}
}

func TestCharDataBoundariesAgainstLegacy(t *testing.T) {
	text := strings.Repeat("한글\r\n&amp;", 6000)
	for _, input := range []string{
		"<r>" + text + "</r>tail",
		"<r>before<![CDATA[" + text + "]]>after</r>",
		"<r><![CDATA[]]><![CDATA[a]]>b&amp;c<![CDATA[d]]></r>",
	} {
		for _, raw := range []bool{false, true} {
			if diff := compareLegacyTokens(input, tokenCfg{strict: true, raw: raw}, true); diff != "" {
				t.Fatal(diff)
			}
		}
	}
}

func TestBenchmarkCatalogEquivalence(t *testing.T) {
	name := os.Getenv("XMLFILE")
	if name == "" {
		t.Skip("set XMLFILE to the catalog benchmark input")
	}
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var want benchCatalog
	if err := stdxml.Unmarshal(data, &want); err != nil {
		t.Fatal(err)
	}
	if len(want.Books) == 0 {
		t.Fatal("benchmark input contains no books")
	}
	wantXML, err := stdxml.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, engine := range []struct {
		name      string
		unmarshal func([]byte, any) error
		marshal   func(any) ([]byte, error)
	}{
		{"stdlib", stdxml.Unmarshal, stdxml.Marshal},
		{"legacy", legacy.Unmarshal, legacy.Marshal},
		{"v1", Unmarshal, Marshal},
	} {
		t.Run(engine.name, func(t *testing.T) {
			var got benchCatalog
			if err := engine.unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("decoded catalog differs from encoding/xml")
			}
			out, err := engine.marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out, wantXML) {
				t.Fatalf("encoded XML differs from encoding/xml: got %d bytes, want %d", len(out), len(wantXML))
			}
			var roundTrip benchCatalog
			if err := stdxml.Unmarshal(out, &roundTrip); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(roundTrip, want) {
				t.Fatal("round trip changed the catalog")
			}
		})
	}
}
