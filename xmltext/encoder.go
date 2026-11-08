// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
	"unsafe"

	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
)

// Encoder writes XML. Elements are opened with BeginElement, given
// attributes with Attr, and the start tag is written lazily when the first
// content (text, child, comment, ...) or the end of the element arrives, so an
// element without content can be written as <a/> and namespace prefixes can
// be chosen with knowledge of the whole tag.
//
// The default (v2) profile validates names, rejects duplicate attributes,
// invalid UTF-8 and characters XML forbids, allows one root element, and
// generates namespace prefixes deterministically (ns1, ns2, ...). Name.Space
// is a namespace URI, never a prefix. A namespace declaration is an attribute
// whose Space is XMLNSNamespace (the
// Decoder reports declarations the same way): see Attr. Any other Space,
// including the string "xmlns", is an ordinary URI. The LegacyOutput option
// switches to the byte-for-byte behavior of encoding/xml, which has its own
// reading of "xmlns" names.
//
// Name strings passed to the Encoder must not be modified afterwards (for
// example a string made with unsafe.String over a reused buffer): validated
// names are remembered by address and length, so a changed string would not be
// validated again.
//
// After the first error every method returns that error and nothing more
// is written. An Encoder is not safe for concurrent use.
type Encoder struct {
	user    any // state of the the v2 package call writing to e; see export.go
	w       io.Writer
	buf     []byte
	limit   int
	flushed int64
	err     error
	closed  bool

	legacy   bool
	frags    bool
	indentOn bool
	ipre     string
	iunit    string

	// pending start tag
	inStart bool
	tname   Name
	tattrs  []encAttr
	tspaced bool     // a pending attribute has a namespace (or is a declaration)
	tdefNS  bool     // put the pending element in the default namespace if free; see export.go
	eager   bool     // the pending start tag is already in buf (no namespace involved yet)
	akeys   []encKey // attributes of an eagerly written tag
	akSetOn bool
	nameOK  [16]string // recently validated names (compared by address and length)
	pbuf    []byte     // escaped attribute values of the pending tag
	attrSet map[encKey]struct{}

	stack  []encFrame
	arena  []byte // written qnames of the open elements
	rooted bool   // a root element has been started
	rootOK bool   // ... and closed (only with a single-root profile)

	// carry for "]]>" detection across Text calls
	rb    int
	rbEnd int64

	// a CDATA section left open by a Partial CData token, and how many ']'
	// (0 to 2) its content ends with
	cdOpen bool
	cdTail int

	// namespaces (v2)
	binds   []nsBind
	pmap    map[string]int32 // prefix -> innermost binding; built when binds > encNSMin
	umap    map[string]int32 // URI -> newest binding of it that is not shadowed
	eused   strSet           // prefixes of the eager tag's attributes (to refuse a later redeclaration)
	defNS   string
	defSave []string // outer default namespaces of the open elements that changed it
	seq     int
	autos   []nsBind
	apre    []string

	// namespaces and indentation (legacy)
	attrNS     map[string]string
	attrPrefix map[string]string
	prefixes   []string
	lseq       int
	ldepth     int
	lindentIn  bool
	lputNL     bool
}

type encKey struct{ space, local string }

type encAttr struct {
	name   Name
	vs, ve int
	raw    string // unescaped value, kept for namespace declarations
}

type encFrame struct {
	name       string // local name of an element whose start tag was written eagerly
	qOff, qLen int32
	nsMark     int32
	prefMark   int32
	seqMark    int32 // e.seq when the element began; restored at its end
	flags      uint8
}

type nsBind struct {
	prefix, uri string
	// used with pmap/umap, -1 for none: the binding of the same prefix this
	// one shadows, and the neighbors (older, newer) in the list of the
	// unshadowed bindings of uri
	prev, uprev, unext int32
}

const (
	fText     = 1 // text or raw output directly inside the element
	fChild    = 2 // element, comment, PI or directive child
	fDefSaved = 4 // the element changed the default namespace; its outer value is on defSave
)

var errClosed = errors.New("use of closed Encoder")

// NewEncoder returns an Encoder writing to w.
func NewEncoder(w io.Writer, opts ...Options) *Encoder {
	e := new(Encoder)
	e.Reset(w, opts...)
	return e
}

// Reset discards all state and starts writing to w, reusing memory.
func (e *Encoder) Reset(w io.Writer, opts ...Options) {
	o := xmlopts.Join(opts...)
	*e = Encoder{
		w:        w,
		buf:      e.buf[:0],
		tattrs:   e.tattrs[:0],
		pbuf:     e.pbuf[:0],
		stack:    e.stack[:0],
		arena:    e.arena[:0],
		binds:    e.binds[:0],
		autos:    e.autos[:0],
		prefixes: e.prefixes[:0],
		akeys:    e.akeys[:0],
		user:     e.user,
		attrSet:  e.attrSet,
		limit:    4096,
		legacy:   o.LegacyOutput,
		ipre:     o.IndentPrefix,
		iunit:    o.IndentUnit,
	}
	if o.Set&xmlopts.HasBufferSize != 0 && o.BufferSize > 0 {
		e.limit = o.BufferSize
	}
	e.indentOn = o.Set&xmlopts.HasIndent != 0 && (o.IndentPrefix != "" || o.IndentUnit != "")
	e.frags = o.AllowFragments
	if o.Set&xmlopts.HasAllowFragments == 0 {
		e.frags = e.legacy
	}
}

// OutputOffset returns the number of bytes produced so far, including
// buffered bytes.
func (e *Encoder) OutputOffset() int64 { return e.flushed + int64(len(e.buf)) }

// Buffered returns the number of bytes waiting to be written to the io.Writer.
func (e *Encoder) Buffered() int { return len(e.buf) }

// StackDepth returns the number of open elements, including one whose start
// tag has not been written yet.
func (e *Encoder) StackDepth() int { return len(e.stack) }

// Err returns the sticky error, if any.
func (e *Encoder) Err() error { return e.err }

func (e *Encoder) fail(err error) error {
	if e.err == nil {
		e.err = err
	}
	return e.err
}

func (e *Encoder) failf(format string, a ...any) error {
	return e.fail(fmt.Errorf("xmltext: "+format, a...))
}

func (e *Encoder) begin() error { return e.err }

// flushBuf writes the buffer to the io.Writer.
func (e *Encoder) flushBuf() error {
	if e.err != nil {
		return e.err
	}
	if len(e.buf) == 0 {
		return nil
	}
	n, err := e.w.Write(e.buf)
	if err == nil && n < len(e.buf) {
		err = io.ErrShortWrite
	}
	if n > len(e.buf) || n < 0 {
		n = 0
	}
	e.flushed += int64(n)
	if err != nil {
		// keep the unwritten tail so a later Flush could retry; the error is sticky
		e.buf = append(e.buf[:0], e.buf[n:]...)
		return e.fail(err)
	}
	e.buf = e.buf[:0]
	return nil
}

func (e *Encoder) maybeFlush() error {
	if len(e.buf) >= e.limit {
		return e.flushBuf()
	}
	return e.err
}

// Flush writes buffered output to the io.Writer. An open start tag stays
// pending: its attributes may still change.
func (e *Encoder) Flush() error {
	if err := e.begin(); err != nil {
		return err
	}
	return e.flushBuf()
}

// Close reports unclosed elements and flushes. It does not close the
// io.Writer. Later writes fail with "use of closed Encoder".
func (e *Encoder) Close() error {
	if e.closed {
		return nil
	}
	e.closed = true
	if e.cdOpen {
		e.endCData()
	}
	defer func() {
		if e.err == nil {
			e.err = errClosed // from now on every call fails
		}
	}()
	if err := e.flushBuf(); err != nil {
		return err
	}
	if len(e.stack) > 0 {
		if e.err != nil {
			return e.err
		}
		f := e.stack[len(e.stack)-1]
		name := string(e.arena[f.qOff : f.qOff+f.qLen])
		switch {
		case f.name != "":
			name = f.name
		case f.qLen == 0: // start tag still pending
			name = e.tname.Local
		}
		return fmt.Errorf("unclosed tag <%s>", name)
	}
	return e.err
}

func unsafeBytes(s string) []byte {
	if s == "" {
		return nil
	}
	return unsafe.Slice(unsafe.StringData(s), len(s))
}

// ---- names ----

func validNameString(s string) bool { return validName(unsafeBytes(s)) }

func (e *Encoder) checkLocal(local, what string) error {
	if e.legacy {
		return nil
	}
	if local != "" {
		// Callers usually pass the same string values over and over (struct
		// field names, interned reader names); a hit on address and length
		// means this exact string was validated before. The cache holds the
		// string, so its memory cannot be reused for different content.
		p := unsafe.StringData(local)
		slot := &e.nameOK[(uintptr(unsafe.Pointer(p))>>3^uintptr(len(local)))&15]
		if unsafe.StringData(*slot) == p && len(*slot) == len(local) {
			return nil
		}
		if validNCName(local) {
			*slot = local
			return nil
		}
	}
	return e.failf("invalid %s name %q", what, local)
}

// validNCName reports whether s is a valid XML name without a colon. Plain
// ASCII names are checked in one table pass.
func validNCName(s string) bool {
	if s == "" {
		return false
	}
	if nameClass[s[0]] == 1 && s[0] != ':' {
		i := 1
		for ; i < len(s); i++ {
			cl := nameClass[s[i]]
			if cl == 0 || cl == 3 || s[i] == ':' {
				break
			}
		}
		if i == len(s) {
			return true
		}
	}
	return validNameString(s) && strings.IndexByte(s, ':') < 0
}

// ---- start tags ----

// BeginElement opens an element. Attributes follow with Attr; the start tag is
// written when the first non-attribute call arrives. name.Space is a
// namespace URI, not a prefix.
func (e *Encoder) BeginElement(name Name) error {
	if err := e.begin(); err != nil {
		return err
	}
	if e.cdOpen {
		e.endCData()
	}
	if e.inStart {
		if err := e.finishStart(false); err != nil {
			return err
		}
	}
	if !e.nameCached(name.Local) {
		if err := e.checkLocal(name.Local, "element"); err != nil {
			return err
		}
	}
	if len(e.stack) == 0 && !e.frags {
		if e.rootOK {
			return e.failf("multiple root elements")
		}
	}
	if len(e.stack) >= xmlopts.DefaultMaxDepth*100 {
		return e.failf("exceeded max depth")
	}
	e.rooted = true
	e.inStart = true
	if !e.legacy && name.Space == "" && e.defNS == "" {
		// No namespace is involved: write the tag name now and the attributes as they come.
		var parent *encFrame
		if n := len(e.stack); n > 0 {
			parent = &e.stack[n-1]
			parent.flags |= fChild
		}
		e.indentOpen(len(e.stack), parent)
		e.buf = append(e.buf, '<')
		e.buf = appendShortString(e.buf, name.Local)
		e.eager = true
		e.akeys = e.akeys[:0]
		e.akSetOn = false
		e.eused.reset()
		e.stack = append(e.stack, encFrame{name: name.Local, qOff: int32(len(e.arena)), nsMark: int32(len(e.binds)), prefMark: int32(len(e.prefixes)), seqMark: int32(e.seq)})
		return nil
	}
	e.eager = false
	e.tname = name
	e.tattrs = e.tattrs[:0]
	e.pbuf = e.pbuf[:0]
	e.tspaced = false
	e.tdefNS = false
	e.stack = append(e.stack, encFrame{nsMark: int32(len(e.binds)), prefMark: int32(len(e.prefixes)), seqMark: int32(e.seq)})
	return nil
}

// isDeclName reports whether n names a namespace declaration (default profile):
// Local is the prefix, or "xmlns" for the default namespace.
func isDeclName(n Name) bool { return n.Space == XMLNSNamespace }

// Attr adds an attribute to the open start tag. A namespace declaration is
// written as Name{Space: XMLNSNamespace, Local: prefix} (Local "xmlns" for the
// default namespace) with the URI as value; an explicit declaration is used
// instead of a generated prefix. Name{Local: "xmlns"} without that Space is
// refused: an attribute named xmlns always declares the default namespace in
// XML, so it must be spelled Name{Space: XMLNSNamespace, Local: "xmlns"}. The
// prefix xml is bound to XMLNamespace and needs no declaration. xml and
// xmlns cannot be declared, nor can their namespaces be bound to another prefix
// (or, for the default namespace, at all).
func (e *Encoder) Attr(name Name, value string) error {
	return e.attr(name, unsafeBytes(value), value, true)
}

// AttrBytes is Attr with a []byte value.
func (e *Encoder) AttrBytes(name Name, value []byte) error {
	return e.attr(name, value, "", false)
}

func (e *Encoder) attr(name Name, value []byte, rawStr string, haveStr bool) error {
	if err := e.begin(); err != nil {
		return err
	}
	if !e.inStart {
		return e.failf("Attr %q outside a start tag", name.Local)
	}
	if e.legacy && name.Local == "" {
		return nil // encoding/xml skips attributes without a name
	}
	if err := e.checkLocal(name.Local, "attribute"); err != nil {
		return err
	}
	if !e.legacy && name.Space == "" && name.Local == "xmlns" {
		return e.failf("attribute xmlns must be a namespace declaration: use Name{Space: XMLNSNamespace, Local: \"xmlns\"} (XMLNSNamespace is %q)", XMLNSNamespace)
	}
	if e.eager {
		return e.eagerAttr(name, value)
	}
	a := encAttr{name: name, vs: len(e.pbuf)}
	if name.Space != "" {
		e.tspaced = true
	}
	var err error
	mode := escAttr
	if e.legacy {
		mode = escLegAttr
	}
	e.pbuf, err = e.escapeInto(e.pbuf, value, mode, 0)
	if err != nil {
		return e.fail(err)
	}
	a.ve = len(e.pbuf)
	if !e.legacy && isDeclName(name) {
		if haveStr {
			a.raw = rawStr
		} else {
			a.raw = string(value)
		}
	}
	e.tattrs = append(e.tattrs, a)
	return nil
}

const encAttrSetMin = 32

// addAttrKey records an attribute of the eagerly written tag and reports
// whether the same (namespace, name) was already there.
func (e *Encoder) addAttrKey(k encKey) bool {
	if !e.akSetOn {
		for i := range e.akeys {
			if e.akeys[i] == k {
				return true
			}
		}
	} else if _, dup := e.attrSet[k]; dup {
		return true
	}
	e.insertAttrKey(k)
	return false
}

// insertAttrKey records a key known not to be there yet.
func (e *Encoder) insertAttrKey(k encKey) {
	e.akeys = append(e.akeys, k)
	if e.akSetOn {
		e.attrSet[k] = struct{}{}
		return
	}
	if len(e.akeys) >= encAttrSetMin {
		if e.attrSet == nil {
			e.attrSet = make(map[encKey]struct{}, 2*encAttrSetMin)
		} else {
			clear(e.attrSet)
		}
		for _, x := range e.akeys {
			e.attrSet[x] = struct{}{}
		}
		e.akSetOn = true
	}
}

// eagerAttr writes an attribute of a start tag whose name is already in buf.
func (e *Encoder) eagerAttr(name Name, value []byte) error {
	if name.Space == "" {
		if e.addAttrKey(encKey{"", name.Local}) {
			return e.failf("duplicate attribute %q", name.Local)
		}
		e.buf = append(e.buf, ' ')
		e.buf = appendShortString(e.buf, name.Local)
		e.buf = append(e.buf, '=', '"')
		var err error
		e.buf, err = e.escapeInto(e.buf, value, escAttr, 0)
		if err != nil {
			return e.fail(err)
		}
		e.buf = append(e.buf, '"')
		return nil
	}
	return e.eagerNSAttr(name, value)
}

// eagerNSAttr handles declarations and namespaced attributes of an eagerly
// written tag; the declarations the encoder generates are written just before
// the attribute that needs them.
func (e *Encoder) eagerNSAttr(name Name, value []byte) error {
	if e.addAttrKey(encKey{name.Space, name.Local}) {
		return e.failf("duplicate attribute %q", name.Local)
	}
	var err error
	switch {
	case name.Space == XMLNSNamespace && name.Local == "xmlns":
		if len(value) != 0 {
			f := e.stack[len(e.stack)-1]
			return e.failf("element <%s> has no namespace and cannot declare the default namespace %q", f.name, value)
		}
		e.buf = append(e.buf, " xmlns=\"\""...)
	case name.Space == XMLNSNamespace:
		p, uri := name.Local, string(value)
		if msg := badDecl(p, uri); msg != "" {
			return e.failf("%s", msg)
		}
		if e.eused.has(p) {
			if old, ok := e.uriOf(p); !ok || old != uri {
				return e.failf("xmlns:%s is declared after an attribute of this element already used the prefix", p)
			}
		}
		e.addBind(p, uri)
		e.buf = append(e.buf, " xmlns:"...)
		e.buf = append(e.buf, p...)
		e.buf = append(e.buf, '=', '"')
		if e.buf, err = e.escapeInto(e.buf, value, escAttr, 0); err != nil {
			return e.fail(err)
		}
		e.buf = append(e.buf, '"')
	default:
		e.autos = e.autos[:0]
		prefix := e.prefixFor(name.Space)
		if len(e.autos) == 0 && prefix != "xml" {
			e.eused.add(prefix)
		}
		for _, d := range e.autos {
			e.insertAttrKey(encKey{XMLNSNamespace, d.prefix}) // a later explicit declaration of it is a duplicate
			e.buf = append(e.buf, " xmlns:"...)
			e.buf = append(e.buf, d.prefix...)
			e.buf = append(e.buf, '=', '"')
			if e.buf, err = e.escapeInto(e.buf, unsafeBytes(d.uri), escAttr, 0); err != nil {
				return e.fail(err)
			}
			e.buf = append(e.buf, '"')
		}
		e.buf = append(e.buf, ' ')
		e.buf = append(e.buf, prefix...)
		e.buf = append(e.buf, ':')
		e.buf = append(e.buf, name.Local...)
		e.buf = append(e.buf, '=', '"')
		if e.buf, err = e.escapeInto(e.buf, value, escAttr, 0); err != nil {
			return e.fail(err)
		}
		e.buf = append(e.buf, '"')
	}
	return nil
}

// finishStart ends the pending start tag, as "/>" when selfClose.
func (e *Encoder) finishStart(selfClose bool) error {
	if !e.eager {
		return e.flushStart(selfClose)
	}
	e.inStart = false
	e.eager = false
	if selfClose {
		e.buf = append(e.buf, '/', '>')
	} else {
		e.buf = append(e.buf, '>')
	}
	return e.maybeFlush()
}

// duplicateAttrs reports a duplicate attribute (same namespace and local name)
// or declaration among the pending attributes.
func (e *Encoder) duplicateAttrs() (string, bool) {
	a := e.tattrs
	if len(a) < encAttrSetMin {
		for i := 1; i < len(a); i++ {
			for j := 0; j < i; j++ {
				if a[i].name == a[j].name {
					return a[i].name.Local, true
				}
			}
		}
		return "", false
	}
	if e.attrSet == nil {
		e.attrSet = make(map[encKey]struct{}, len(a))
	} else {
		clear(e.attrSet)
	}
	for i := range a {
		k := encKey{a[i].name.Space, a[i].name.Local}
		if _, dup := e.attrSet[k]; dup {
			return k.local, true
		}
		e.attrSet[k] = struct{}{}
	}
	return "", false
}

// lookupURI returns the prefix bound to uri and not shadowed, if any.
func (e *Encoder) lookupURI(uri string) (string, bool) {
	if e.umap != nil {
		if i, ok := e.umap[uri]; ok {
			return e.binds[i].prefix, true
		}
		return "", false
	}
next:
	for i := len(e.binds) - 1; i >= 0; i-- {
		b := e.binds[i]
		if b.uri != uri {
			continue
		}
		for j := len(e.binds) - 1; j > i; j-- {
			if e.binds[j].prefix == b.prefix {
				continue next
			}
		}
		return b.prefix, true
	}
	return "", false
}

// uriOf returns the URI the prefix is currently bound to.
func (e *Encoder) uriOf(p string) (string, bool) {
	if e.pmap != nil {
		if i, ok := e.pmap[p]; ok {
			return e.binds[i].uri, true
		}
		return "", false
	}
	for i := len(e.binds) - 1; i >= 0; i-- {
		if e.binds[i].prefix == p {
			return e.binds[i].uri, true
		}
	}
	return "", false
}

func (e *Encoder) prefixBound(p string) bool {
	if e.pmap != nil {
		_, ok := e.pmap[p]
		return ok
	}
	for i := len(e.binds) - 1; i >= 0; i-- {
		if e.binds[i].prefix == p {
			return true
		}
	}
	return false
}

// encNSMin is the number of live bindings above which prefix and URI lookups
// use indexes instead of scanning, keeping tags with tens of thousands of
// declarations linear (cf. the Decoder's nsMapMin). A variable so tests can
// compare indexed and scanned lookups.
var encNSMin = 16

// addBind records a binding, indexing it once there are many.
func (e *Encoder) addBind(prefix, uri string) {
	e.binds = append(e.binds, nsBind{prefix: prefix, uri: uri, prev: -1, uprev: -1, unext: -1})
	if e.pmap != nil {
		e.indexBind(int32(len(e.binds) - 1))
	} else if len(e.binds) > encNSMin {
		e.pmap = make(map[string]int32, 2*len(e.binds))
		e.umap = make(map[string]int32, 2*len(e.binds))
		for i := range e.binds {
			e.indexBind(int32(i))
		}
	}
}

// indexBind makes binding i the innermost of its prefix and the newest
// unshadowed one of its URI. The binding it shadows leaves its URI's list,
// keeping its own links so that truncBinds can put it back (the bindings are
// removed in the reverse order they were added, as in dancing links).
func (e *Encoder) indexBind(i int32) {
	b := &e.binds[i]
	if h, ok := e.pmap[b.prefix]; ok {
		b.prev = h
		e.unlinkURI(h)
	}
	e.pmap[b.prefix] = i
	if h, ok := e.umap[b.uri]; ok {
		b.uprev = h
		e.binds[h].unext = i
	}
	e.umap[b.uri] = i
}

// unlinkURI takes binding i out of the list of unshadowed bindings of its URI.
func (e *Encoder) unlinkURI(i int32) {
	b := &e.binds[i]
	switch {
	case b.unext >= 0:
		e.binds[b.unext].uprev = b.uprev
	case b.uprev >= 0:
		e.umap[b.uri] = b.uprev
	default:
		delete(e.umap, b.uri)
	}
	if b.uprev >= 0 {
		e.binds[b.uprev].unext = b.unext
	}
}

// relinkURI puts binding i back where unlinkURI took it from; the list is
// as it was right after that.
func (e *Encoder) relinkURI(i int32) {
	b := &e.binds[i]
	if b.unext >= 0 {
		e.binds[b.unext].uprev = i
	} else {
		e.umap[b.uri] = i
	}
	if b.uprev >= 0 {
		e.binds[b.uprev].unext = i
	}
}

// truncBinds drops the bindings declared after mark (a closed element's).
func (e *Encoder) truncBinds(mark int) {
	if e.pmap != nil {
		for i := len(e.binds) - 1; i >= mark; i-- {
			b := &e.binds[i]
			e.unlinkURI(int32(i))
			if b.prev < 0 {
				delete(e.pmap, b.prefix)
			} else {
				e.relinkURI(b.prev)
				e.pmap[b.prefix] = b.prev
			}
		}
	}
	e.binds = e.binds[:mark]
}

// strSet is a small set of strings: a list, and a map once it is long.
type strSet struct {
	list []string
	set  map[string]struct{}
}

const strSetMin = 32

func (s *strSet) reset() {
	s.list = s.list[:0]
	if s.set != nil {
		clear(s.set)
	}
}

func (s *strSet) has(x string) bool {
	if len(s.list) >= strSetMin {
		_, ok := s.set[x]
		return ok
	}
	for _, y := range s.list {
		if y == x {
			return true
		}
	}
	return false
}

func (s *strSet) add(x string) {
	if s.has(x) {
		return
	}
	s.list = append(s.list, x)
	switch {
	case len(s.list) == strSetMin:
		if s.set == nil {
			s.set = make(map[string]struct{}, 2*strSetMin)
		}
		for _, y := range s.list {
			s.set[y] = struct{}{}
		}
	case len(s.list) > strSetMin:
		s.set[x] = struct{}{}
	}
}

// prefixFor returns a prefix for uri, generating and recording a declaration
// when none is in scope.
func (e *Encoder) prefixFor(uri string) string {
	if uri == XMLNamespace {
		return "xml"
	}
	if p, ok := e.lookupURI(uri); ok {
		return p
	}
	for {
		e.seq++
		p := genPrefix(e.seq)
		if e.prefixBound(p) {
			continue
		}
		e.addBind(p, uri)
		e.autos = append(e.autos, nsBind{prefix: p, uri: uri})
		return p
	}
}

// genPrefixes holds the first generated prefixes so that the common
// shallow cases need no allocation. A literal is initialized statically.
var genPrefixes = [...]string{
	"ns1", "ns2", "ns3", "ns4", "ns5", "ns6", "ns7", "ns8",
	"ns9", "ns10", "ns11", "ns12", "ns13", "ns14", "ns15", "ns16",
	"ns17", "ns18", "ns19", "ns20", "ns21", "ns22", "ns23", "ns24",
	"ns25", "ns26", "ns27", "ns28", "ns29", "ns30", "ns31", "ns32",
	"ns33", "ns34", "ns35", "ns36", "ns37", "ns38", "ns39", "ns40",
	"ns41", "ns42", "ns43", "ns44", "ns45", "ns46", "ns47", "ns48",
	"ns49", "ns50", "ns51", "ns52", "ns53", "ns54", "ns55", "ns56",
	"ns57", "ns58", "ns59", "ns60", "ns61", "ns62", "ns63", "ns64",
}

// genPrefix returns the generated prefix number n, counting from 1.
func genPrefix(n int) string {
	if n <= len(genPrefixes) {
		return genPrefixes[n-1]
	}
	return string(strconv.AppendInt([]byte("ns"), int64(n), 10))
}

// flushStart writes the pending start tag. selfClose ends it with "/>".
func (e *Encoder) flushStart(selfClose bool) error {
	e.inStart = false
	f := &e.stack[len(e.stack)-1]
	var parent *encFrame
	if len(e.stack) > 1 {
		parent = &e.stack[len(e.stack)-2]
	}
	if e.legacy {
		e.startLegacy(f)
		return e.maybeFlush()
	}
	if parent != nil {
		parent.flags |= fChild
	}
	e.indentOpen(len(e.stack)-1, parent)
	if err := e.startV2(f, selfClose); err != nil {
		return e.fail(err)
	}
	return e.maybeFlush()
}

func (e *Encoder) startV2(f *encFrame, selfClose bool) error {
	if len(e.tattrs) > 1 {
		if a, dup := e.duplicateAttrs(); dup {
			return fmt.Errorf("xmltext: duplicate attribute %q", a)
		}
	}
	if !e.tspaced && e.tname.Space == "" && e.defNS == "" {
		// no namespaces involved: nothing to resolve or declare
		name := e.tname.Local
		f.qOff = int32(len(e.arena))
		f.qLen = int32(len(name))
		e.arena = appendShortString(e.arena, name)
		e.buf = append(e.buf, '<')
		e.buf = appendShortString(e.buf, name)
		for i := range e.tattrs {
			a := &e.tattrs[i]
			e.buf = append(e.buf, ' ')
			e.buf = appendShortString(e.buf, a.name.Local)
			e.buf = append(e.buf, '=', '"')
			e.buf = appendShort(e.buf, e.pbuf[a.vs:a.ve])
			e.buf = append(e.buf, '"')
		}
		if selfClose {
			e.buf = append(e.buf, '/', '>')
		} else {
			e.buf = append(e.buf, '>')
		}
		return nil
	}
	e.autos = e.autos[:0]
	// explicit declarations first: they decide the prefixes
	declDef := false
	for i := range e.tattrs {
		a := &e.tattrs[i]
		if !isDeclName(a.name) {
			continue
		}
		if a.name.Local == "xmlns" { // default namespace
			if msg := badDecl("", a.raw); msg != "" {
				return errors.New("xmltext: " + msg)
			}
			e.saveDef(f)
			e.defNS = a.raw
			declDef = true
			continue
		}
		if msg := badDecl(a.name.Local, a.raw); msg != "" {
			return errors.New("xmltext: " + msg)
		}
		e.addBind(a.name.Local, a.raw)
	}
	name := e.tname
	var qpre string
	switch {
	case name.Space == "":
		if e.defNS != "" && declDef {
			return fmt.Errorf("xmltext: element <%s> has no namespace and cannot declare the default namespace %q", name.Local, e.defNS)
		}
		if e.defNS != "" {
			e.autos = append(e.autos, nsBind{}) // xmlns="" so the element stays unqualified
			e.saveDef(f)
			e.defNS = ""
		}
	case name.Space == XMLNSNamespace:
		return fmt.Errorf("xmltext: element <%s> cannot be in the namespace %s, which is reserved for declarations", name.Local, XMLNSNamespace)
	case name.Space == e.defNS:
	default:
		if p, ok := e.lookupURI(name.Space); ok {
			qpre = p
		} else if e.tdefNS && e.defNS == "" && !declDef && name.Space != XMLNamespace {
			e.autos = append(e.autos, nsBind{uri: name.Space})
			e.saveDef(f)
			e.defNS = name.Space
		} else {
			qpre = e.prefixFor(name.Space)
		}
	}
	// attribute prefixes, in order
	var apre []string
	if cap(e.apre) < len(e.tattrs) {
		e.apre = make([]string, len(e.tattrs))
	}
	apre = e.apre[:len(e.tattrs)]
	for i := range e.tattrs {
		a := &e.tattrs[i]
		apre[i] = ""
		if isDeclName(a.name) || a.name.Space == "" {
			continue
		}
		apre[i] = e.prefixFor(a.name.Space)
	}
	// write
	f.qOff = int32(len(e.arena))
	if qpre != "" {
		e.arena = append(e.arena, qpre...)
		e.arena = append(e.arena, ':')
	}
	e.arena = append(e.arena, name.Local...)
	f.qLen = int32(len(e.arena)) - f.qOff
	e.buf = append(e.buf, '<')
	e.buf = append(e.buf, e.arena[f.qOff:f.qOff+f.qLen]...)
	for i := range e.tattrs {
		a := &e.tattrs[i]
		if !isDeclName(a.name) {
			continue
		}
		e.buf = append(e.buf, " xmlns"...)
		if a.name.Local != "xmlns" {
			e.buf = append(e.buf, ':')
			e.buf = append(e.buf, a.name.Local...)
		}
		e.buf = append(e.buf, '=', '"')
		e.buf = append(e.buf, e.pbuf[a.vs:a.ve]...)
		e.buf = append(e.buf, '"')
	}
	for _, d := range e.autos {
		e.buf = append(e.buf, " xmlns"...)
		if d.prefix != "" {
			e.buf = append(e.buf, ':')
			e.buf = append(e.buf, d.prefix...)
		}
		e.buf = append(e.buf, '=', '"')
		var err error
		e.buf, err = e.escapeInto(e.buf, unsafeBytes(d.uri), escAttr, 0)
		if err != nil {
			return err
		}
		e.buf = append(e.buf, '"')
	}
	for i := range e.tattrs {
		a := &e.tattrs[i]
		if isDeclName(a.name) {
			continue
		}
		e.buf = append(e.buf, ' ')
		if apre[i] != "" {
			e.buf = append(e.buf, apre[i]...)
			e.buf = append(e.buf, ':')
		}
		e.buf = append(e.buf, a.name.Local...)
		e.buf = append(e.buf, '=', '"')
		e.buf = append(e.buf, e.pbuf[a.vs:a.ve]...)
		e.buf = append(e.buf, '"')
	}
	if selfClose {
		e.buf = append(e.buf, '/', '>')
	} else {
		e.buf = append(e.buf, '>')
	}
	return nil
}

// ---- end tags ----

// EndElement closes the current element, as <a/> when it has no content.
func (e *Encoder) EndElement() error {
	if err := e.begin(); err != nil {
		return err
	}
	if e.cdOpen {
		e.endCData()
	}
	if len(e.stack) == 0 {
		return e.failf("EndElement without an open element")
	}
	if e.inStart {
		if err := e.finishStart(!e.legacy); err != nil {
			return err
		}
		if !e.legacy {
			e.popFrame() // written as <a/>
			return e.err
		}
	}
	f := &e.stack[len(e.stack)-1]
	if e.legacy {
		e.writeIndentLegacy(-1)
	} else {
		e.indentClose(f, len(e.stack)-1)
	}
	e.buf = append(e.buf, '<', '/')
	if f.name != "" {
		e.buf = appendShortString(e.buf, f.name)
	} else {
		e.buf = appendShort(e.buf, e.arena[f.qOff:f.qOff+f.qLen])
	}
	e.buf = append(e.buf, '>')
	e.popFrame()
	return e.maybeFlush()
}

func (e *Encoder) popFrame() {
	f := &e.stack[len(e.stack)-1]
	e.stack = e.stack[:len(e.stack)-1]
	e.arena = e.arena[:f.qOff]
	// Generated prefixes go out of scope with the element, so siblings
	// reuse the same numbers instead of counting up through the document.
	e.seq = int(f.seqMark)
	if e.legacy {
		e.popPrefix(int(f.prefMark))
	} else {
		if len(e.binds) > int(f.nsMark) {
			e.truncBinds(int(f.nsMark))
		}
		if f.flags&fDefSaved != 0 {
			e.defNS = e.defSave[len(e.defSave)-1]
			e.defSave = e.defSave[:len(e.defSave)-1]
		}
	}
	if len(e.stack) == 0 {
		e.rootOK = true
	}
}

// ---- legacy start/namespace/indent (encoding/xml compatible) ----

func (e *Encoder) startLegacy(f *encFrame) {
	e.prefixes = append(e.prefixes, "") // markPrefix
	f.prefMark = int32(len(e.prefixes) - 1)
	e.writeIndentLegacy(1)
	name := e.tname
	f.qOff = int32(len(e.arena))
	e.arena = append(e.arena, name.Local...)
	f.qLen = int32(len(name.Local))
	e.buf = append(e.buf, '<')
	e.buf = append(e.buf, name.Local...)
	if name.Space != "" {
		e.buf = append(e.buf, " xmlns=\""...)
		e.buf, _ = e.escapeInto(e.buf, unsafeBytes(name.Space), escLegAttr, 0)
		e.buf = append(e.buf, '"')
	}
	for i := range e.tattrs {
		a := &e.tattrs[i]
		e.buf = append(e.buf, ' ')
		if a.name.Space != "" {
			e.buf = append(e.buf, e.createAttrPrefix(a.name.Space)...)
			e.buf = append(e.buf, ':')
		}
		e.buf = append(e.buf, a.name.Local...)
		e.buf = append(e.buf, '=', '"')
		e.buf = append(e.buf, e.pbuf[a.vs:a.ve]...)
		e.buf = append(e.buf, '"')
	}
	e.buf = append(e.buf, '>')
}

func (e *Encoder) createAttrPrefix(url string) string {
	if prefix := e.attrPrefix[url]; prefix != "" {
		return prefix
	}
	if url == XMLNamespace {
		return "xml"
	}
	if e.attrPrefix == nil {
		e.attrPrefix = make(map[string]string)
		e.attrNS = make(map[string]string)
	}
	prefix := strings.TrimRight(url, "/")
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		prefix = prefix[i+1:]
	}
	if prefix == "" || !legacyIsName(prefix) || strings.Contains(prefix, ":") {
		prefix = "_"
	}
	if len(prefix) >= 3 && strings.EqualFold(prefix[:3], "xml") {
		prefix = "_" + prefix
	}
	if e.attrNS[prefix] != "" {
		for e.lseq++; ; e.lseq++ {
			if id := prefix + "_" + strconv.Itoa(e.lseq); e.attrNS[id] == "" {
				prefix = id
				break
			}
		}
	}
	e.attrPrefix[url] = prefix
	e.attrNS[prefix] = url
	e.buf = append(e.buf, "xmlns:"...)
	e.buf = append(e.buf, prefix...)
	e.buf = append(e.buf, '=', '"')
	e.buf, _ = e.escapeInto(e.buf, unsafeBytes(url), escLegText, 0)
	e.buf = append(e.buf, '"', ' ')
	e.prefixes = append(e.prefixes, prefix)
	return prefix
}

func (e *Encoder) popPrefix(mark int) {
	for len(e.prefixes) > mark {
		prefix := e.prefixes[len(e.prefixes)-1]
		e.prefixes = e.prefixes[:len(e.prefixes)-1]
		if prefix == "" {
			continue
		}
		delete(e.attrPrefix, e.attrNS[prefix])
		delete(e.attrNS, prefix)
	}
}

func (e *Encoder) writeIndentLegacy(delta int) {
	if e.ipre == "" && e.iunit == "" {
		return
	}
	if delta < 0 {
		e.ldepth--
		if e.lindentIn {
			e.lindentIn = false
			return
		}
		e.lindentIn = false
	}
	if e.lputNL {
		e.buf = append(e.buf, '\n')
	} else {
		e.lputNL = true
	}
	e.buf = append(e.buf, e.ipre...)
	for i := 0; i < e.ldepth; i++ {
		e.buf = append(e.buf, e.iunit...)
	}
	if delta > 0 {
		e.ldepth++
		e.lindentIn = true
	}
}

// legacyIsName is encoding/xml's name test used for derived attribute
// prefixes. Prefixes come from URLs, so the fifth-edition tables of
// validName are used; they agree with v1 on every name v1 accepts that can
// appear as a URL segment prefix in practice.
func legacyIsName(s string) bool { return validNameString(s) }

// ---- v2 indentation ----

func (e *Encoder) indentOpen(depth int, parent *encFrame) {
	if !e.indentOn || parent != nil && parent.flags&fText != 0 {
		return
	}
	if e.OutputOffset() > 0 {
		e.buf = append(e.buf, '\n')
	}
	e.buf = append(e.buf, e.ipre...)
	for i := 0; i < depth; i++ {
		e.buf = append(e.buf, e.iunit...)
	}
}

func (e *Encoder) indentClose(f *encFrame, depth int) {
	if !e.indentOn || f.flags&fText != 0 || f.flags&fChild == 0 {
		return
	}
	e.buf = append(e.buf, '\n')
	e.buf = append(e.buf, e.ipre...)
	for i := 0; i < depth; i++ {
		e.buf = append(e.buf, e.iunit...)
	}
}

// ---- content ----

// content prepares for non-attribute output: closes the pending start tag.
func (e *Encoder) content() error {
	if err := e.begin(); err != nil {
		return err
	}
	if e.cdOpen {
		e.endCData()
	}
	if e.inStart {
		return e.finishStart(false)
	}
	return nil
}

// endCData closes a CDATA section left open by a Partial CData token.
func (e *Encoder) endCData() {
	e.buf = append(e.buf, "]]>"...)
	e.cdOpen, e.cdTail = false, 0
}

func (e *Encoder) top() *encFrame {
	if len(e.stack) == 0 {
		return nil
	}
	return &e.stack[len(e.stack)-1]
}

func isAllSpace(b []byte) bool {
	for _, c := range b {
		if !isSpace(c) {
			return false
		}
	}
	return true
}

// Text writes character data, escaping it.
func (e *Encoder) Text(s string) error { return e.TextBytes(unsafeBytes(s)) }

// TextBytes is Text with a []byte value.
func (e *Encoder) TextBytes(b []byte) error {
	mode := escText
	if e.legacy {
		mode = escLegText
	}
	return e.text(b, mode)
}

// LegacyText writes character data as encoding/xml writes an element's string
// content: like Text, but with newlines escaped when escapeNewline is set.
// It exists for the v1 compatibility layer.
func (e *Encoder) LegacyText(b []byte, escapeNewline bool) error {
	mode := escLegText
	if escapeNewline {
		mode = escLegTextNL
	}
	return e.text(b, mode)
}

func (e *Encoder) text(b []byte, mode int) error {
	if err := e.content(); err != nil {
		return err
	}
	if len(e.stack) == 0 && !e.frags && !isAllSpace(b) {
		return e.failf("text outside of the root element")
	}
	if f := e.top(); f != nil {
		f.flags |= fText
	}
	carry := 0
	if e.rbEnd == e.OutputOffset() {
		carry = e.rb
	}
	if mode == escText && len(b) <= 8 && shortTextPlain(b) {
		e.buf = appendShort(e.buf, b)
	} else {
		var err error
		e.buf, err = e.escapeInto(e.buf, b, mode, carry)
		if err != nil {
			return e.fail(err)
		}
	}
	e.rb, e.rbEnd = 0, e.OutputOffset()
	if n := len(b); n > 0 && b[n-1] == ']' {
		e.rb = 1
		if n > 1 && b[n-2] == ']' || carry == 1 && n == 1 {
			e.rb = 2
		}
		if carry == 2 {
			e.rb = 2
		}
	}
	return e.maybeFlush()
}

// CData writes a CDATA section; "]]>" inside b is split across sections. An
// empty b writes nothing. WriteToken of a Partial CData token leaves its
// section open for the following pieces; a CData call then continues that
// section and ends it.
func (e *Encoder) CData(b []byte) error { return e.cdata(b, false) }

// cdata writes b as CDATA. With partial the section stays open for the next
// cdata call; any other write closes it first. A "]]>" formed by the end of
// one piece and the start of the next is split like one inside a piece.
func (e *Encoder) cdata(b []byte, partial bool) error {
	open := e.cdOpen
	if open {
		if err := e.begin(); err != nil {
			return err
		}
	} else {
		if err := e.content(); err != nil {
			return err
		}
		if len(b) == 0 && !partial {
			return nil
		}
		if len(e.stack) == 0 && !e.frags {
			return e.failf("CDATA outside of the root element")
		}
	}
	if !e.legacy {
		if err := e.checkChars(b); err != nil {
			return e.fail(err)
		}
	}
	tail := e.cdTail
	if open {
		switch {
		case tail == 2 && len(b) > 0 && b[0] == '>':
			e.buf = append(e.buf, "]]><![CDATA["...)
			tail = 0
		case tail >= 1 && len(b) > 1 && b[0] == ']' && b[1] == '>':
			e.buf = append(e.buf, "]]]><![CDATA["...)
			b, tail = b[1:], 0
		}
	} else {
		if f := e.top(); f != nil {
			f.flags |= fText
		}
		e.buf = append(e.buf, "<![CDATA["...)
		e.cdOpen, tail = true, 0
	}
	for {
		before, after, ok := bytes.Cut(b, []byte("]]>"))
		if !ok {
			break
		}
		e.buf = append(e.buf, before...)
		e.buf = append(e.buf, "]]]]><![CDATA[>"...)
		b, tail = after, 0
	}
	e.buf = append(e.buf, b...)
	n := 0
	for n < len(b) && n < 2 && b[len(b)-1-n] == ']' {
		n++
	}
	if n == len(b) { // only ']': the previous tail continues
		n = min(n+tail, 2)
	}
	e.cdTail = n
	if !partial {
		e.endCData()
	}
	return e.maybeFlush()
}

// checkChars reports invalid UTF-8 or a character XML forbids in b.
func (e *Encoder) checkChars(b []byte) error {
	for i := 0; i < len(b); {
		c := b[i]
		if c < utf8.RuneSelf {
			if c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
				return fmt.Errorf("xmltext: invalid character %#U", rune(c))
			}
			i++
			continue
		}
		r, n := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && n == 1 || !validXMLRune(r) {
			return fmt.Errorf("xmltext: invalid UTF-8 or character at byte %d", i)
		}
		i += n
	}
	return nil
}

// Comment writes <!--b-->.
func (e *Encoder) Comment(b []byte) error {
	if err := e.content(); err != nil {
		return err
	}
	if !e.legacy {
		if bytes.Contains(b, []byte("--")) || len(b) > 0 && b[len(b)-1] == '-' {
			return e.failf(`comment contains "--" or ends with "-"`)
		}
		if err := e.checkChars(b); err != nil {
			return e.fail(err)
		}
	}
	e.markChild()
	e.buf = append(e.buf, "<!--"...)
	e.buf = append(e.buf, b...)
	if e.legacy && len(b) > 0 && b[len(b)-1] == '-' {
		e.buf = append(e.buf, ' ') // "--->" is invalid; v1 writes "- -->"
	}
	e.buf = append(e.buf, "-->"...)
	return e.maybeFlush()
}

// ProcInst writes <?target inst?>. The default profile rejects colons in the
// target. The target "xml" in any letter case is reserved: only the exact
// "xml" is allowed, as the first output (the XML declaration), and its inst
// must be version 1.0 with optional encoding and standalone, as the Decoder requires.
func (e *Encoder) ProcInst(target string, inst []byte) error {
	if err := e.content(); err != nil {
		return err
	}
	if !e.legacy {
		if !validNameString(target) {
			return e.failf("invalid processing instruction target %q", target)
		}
		if strings.Contains(target, ":") {
			return e.failf("processing instruction target %q contains a colon", target)
		}
		if strings.EqualFold(target, "xml") {
			if target != "xml" || e.OutputOffset() != 0 {
				return e.failf("xml declaration only valid as the first output")
			}
			// the Decoder accepts only a well-formed declaration with version 1.0
			decl := bytes.TrimLeft(inst, " \t\r\n")
			version, _, _, msg := parseXMLDecl(decl)
			if msg != "" {
				return e.failf("invalid xml declaration %q: %s", inst, msg)
			}
			if version != "1.0" {
				return e.failf("invalid xml declaration %q: unsupported version %q", inst, version)
			}
		}
		if bytes.Contains(inst, []byte("?>")) {
			return e.failf(`processing instruction contains "?>"`)
		}
		if err := e.checkChars(inst); err != nil {
			return e.fail(err)
		}
	}
	e.markChild()
	e.buf = append(e.buf, "<?"...)
	e.buf = append(e.buf, target...)
	if len(inst) > 0 {
		e.buf = append(e.buf, ' ')
		e.buf = append(e.buf, inst...)
	}
	e.buf = append(e.buf, "?>"...)
	return e.maybeFlush()
}

// Directive writes <!b>. In the default profile its angle brackets must be
// balanced outside comments and quotes.
func (e *Encoder) Directive(b []byte) error {
	if err := e.content(); err != nil {
		return err
	}
	if !e.legacy {
		if !validDirective(b) {
			return e.failf("directive contains unbalanced < or >")
		}
		if err := e.checkChars(b); err != nil {
			return e.fail(err)
		}
	}
	e.markChild()
	e.buf = append(e.buf, "<!"...)
	e.buf = append(e.buf, b...)
	e.buf = append(e.buf, '>')
	return e.maybeFlush()
}

// markChild records a non-element node in the open element (indentation) and
// indents it.
func (e *Encoder) markChild() {
	f := e.top()
	if e.legacy {
		return
	}
	if f != nil {
		f.flags |= fChild
	}
	e.indentOpen(len(e.stack), f)
}

// WriteRaw writes b unchanged and unchecked (for innerxml).
func (e *Encoder) WriteRaw(b []byte) error {
	if err := e.content(); err != nil {
		return err
	}
	if f := e.top(); f != nil {
		f.flags |= fText
	}
	e.buf = append(e.buf, b...)
	return e.maybeFlush()
}

// WriteToken writes a token obtained from a Decoder.
func (e *Encoder) WriteToken(t Token) error {
	switch t.Kind() {
	case StartElement:
		if err := e.BeginElement(t.Name()); err != nil {
			return err
		}
		// Declarations first, so a prefix declared later in the tag is used
		// instead of a generated one.
		attrs := t.Attrs()
		for it := attrs; it.Next(); {
			if it.isDecl() {
				if err := e.AttrBytes(it.Name(), it.Value()); err != nil {
					return err
				}
			}
		}
		for it := attrs; it.Next(); {
			if !it.isDecl() {
				if err := e.AttrBytes(it.Name(), it.Value()); err != nil {
					return err
				}
			}
		}
		return nil
	case EndElement:
		return e.EndElement()
	case Text:
		return e.TextBytes(t.Text())
	case CData:
		return e.cdata(t.Text(), t.Partial())
	case Comment:
		return e.Comment(t.Text())
	case ProcInst:
		return e.ProcInst(t.Name().Local, t.Text())
	case Directive:
		return e.Directive(t.Text())
	}
	return e.failf("WriteToken: invalid token kind")
}

// validDirective reports whether the angle brackets of dir are balanced,
// ignoring comments and quoted strings (the check encoding/xml applies). As in
// the Decoder's scan, a comment ends at a "-->" that starts at least four bytes
// after its "<!--", so "<!-->" does not close itself.
func validDirective(dir []byte) bool {
	var (
		depth     int
		inquote   byte
		incomment bool
		cstart    int // index of the "<" opening the comment
	)
	for i, c := range dir {
		switch {
		case incomment:
			if c == '>' && i-2 >= cstart+4 && dir[i-1] == '-' && dir[i-2] == '-' {
				incomment = false
			}
		case inquote != 0:
			if c == inquote {
				inquote = 0
			}
		case c == '\'' || c == '"':
			inquote = c
		case c == '<':
			if i+4 < len(dir) && bytes.Equal(dir[i:i+4], []byte("<!--")) {
				incomment, cstart = true, i
			} else {
				depth++
			}
		case c == '>':
			if depth == 0 {
				return false
			}
			depth--
		}
	}
	return depth == 0 && inquote == 0 && !incomment
}

// appendShort appends s to dst; short slices are copied with a byte loop,
// which beats the memmove call for the one- to eight-byte names and values
// that dominate tag-heavy output.
func appendShort(dst, s []byte) []byte {
	n := len(dst)
	if len(s) <= 8 && cap(dst)-n >= len(s) {
		dst = dst[:n+len(s)]
		for i := range s {
			dst[n+i] = s[i]
		}
		return dst
	}
	return append(dst, s...)
}

// appendShortString is appendShort for a string.
func appendShortString(dst []byte, s string) []byte {
	n := len(dst)
	if len(s) <= 8 && cap(dst)-n >= len(s) {
		dst = dst[:n+len(s)]
		for i := 0; i < len(s); i++ {
			dst[n+i] = s[i]
		}
		return dst
	}
	return append(dst, s...)
}

// Indent sets the indentation of an Encoder that is already created. See
// WithIndent.
func (e *Encoder) Indent(prefix, unit string) {
	e.ipre, e.iunit = prefix, unit
	e.indentOn = prefix != "" || unit != ""
}

// CloseStartTag writes the pending start tag now, so it is part of the output
// (and of a Flush) before any content follows. Attr is not allowed afterwards.
func (e *Encoder) CloseStartTag() error {
	if err := e.begin(); err != nil {
		return err
	}
	if e.inStart {
		return e.finishStart(false)
	}
	return nil
}

// LegacyIndent writes encoding/xml's indentation at the current depth for a
// node that is neither a start nor an end tag (delta 0). It exists for the v1
// compatibility layer and does nothing outside LegacyOutput mode.
func (e *Encoder) LegacyIndent(delta int) {
	if e.legacy && e.err == nil {
		e.writeIndentLegacy(delta)
	}
}

// saveDef remembers the default namespace before frame f changes it.
func (e *Encoder) saveDef(f *encFrame) {
	if f.flags&fDefSaved == 0 {
		e.defSave = append(e.defSave, e.defNS)
		f.flags |= fDefSaved
	}
}

// textPlainByte marks bytes that character data may carry unescaped in any
// position: printable ASCII except & < > and \r (tab and newline are kept).
var textPlainByte = func() (t [256]bool) {
	for c := 0x20; c < 0x7f; c++ {
		t[c] = true
	}
	t['&'], t['<'], t['>'] = false, false, false
	t['\t'], t['\n'] = true, true
	return
}()

func shortTextPlain(b []byte) bool {
	for _, c := range b {
		if !textPlainByte[c] {
			return false
		}
	}
	return true
}

// nameCached reports whether l is a string value validated before (same
// address and length), see checkLocal.
func (e *Encoder) nameCached(l string) bool {
	if l == "" {
		return false
	}
	p := unsafe.StringData(l)
	c := e.nameOK[(uintptr(unsafe.Pointer(p))>>3^uintptr(len(l)))&15]
	return unsafe.StringData(c) == p && len(c) == len(l)
}
