package xml

import (
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

	legacy "github.com/Clickin/go-experiment-xml/internal/legacyxml"
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
