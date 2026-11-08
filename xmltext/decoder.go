// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"sync"
	"unicode/utf8"

	"github.com/go-xml-experiment/xml/internal/xmlopts"
)

const (
	// XMLNamespace is the namespace bound to the prefix xml.
	XMLNamespace = "http://www.w3.org/XML/1998/namespace"

	// XMLNSNamespace is the namespace of namespace declaration attributes:
	// Name{Space: XMLNSNamespace, Local: prefix} is xmlns:prefix, and
	// Name{Space: XMLNSNamespace, Local: "xmlns"} is the default declaration.
	XMLNSNamespace = "http://www.w3.org/2000/xmlns/"
)

const (
	// minRead is the read window of a Reader that has proven to have more
	// input: the buffer ramps up to it and grows by it. It matches the
	// engine's text window.
	minRead = 32 << 10
	// initialRead is the first buffer of a Reader that does not report its
	// length. A small document never needs more; a larger one reaches minRead
	// at its first full read.
	initialRead = 4 << 10
	// minInitialRead is the smallest first buffer, used when the reader says
	// it holds almost nothing.
	minInitialRead = 64
)

// lowRoom is the free space fill wants in a buffer of capacity c before it
// reads: half a window once the buffer has reached minRead, half the buffer
// below that.
func lowRoom(c int) int { return min(c, minRead) / 2 }

// firstBuf is the capacity of the first buffer for reading from r. A reader
// that reports its remaining length (a Len method) gets that length plus one,
// at least minInitialRead and at most one window; without a Len the buffer
// starts at initialRead and ramps up if the input keeps filling it.
func firstBuf(r io.Reader) int {
	if l, ok := r.(interface{ Len() int }); ok {
		if n := l.Len(); n >= 0 {
			return min(max(n+1, minInitialRead), minRead)
		}
	}
	return initialRead
}

// Name is a namespace-resolved XML name.
type Name struct{ Space, Local string }

// Attr is an attribute with a materialized value.
type Attr struct {
	Name  Name
	Value string
}

// Raw is the unparsed bytes of one element, valid until the next Decoder call.
type Raw []byte

type attrSpan struct {
	name, colon, nameEnd int // colon is the index of ':' in the qname or -1
	val, valEnd          int
	verbatim             bool
	decl                 bool // xmlns or xmlns:prefix
}

type stackEntry struct {
	nameOff, nameLen int    // qname bytes in nameArena
	colon            int    // offset of ':' within the qname, or -1
	local            string // interned local name, filled on first Name() or RawName()
	nsMark           int    // len(bindings) before this element's declarations
	defaultNS        string
	nsChanged        bool // v1 keeps namespace undo records on a loose mismatch
}

type binding struct {
	prefix, uri string
	prev        int // previous binding of the same prefix (index), or -1; used with nsMap
}

// Decoder reads XML tokens. Token values it returns are views into
// decoder-owned memory and are valid only until the next call to ReadToken,
// PeekKind, ReadElement, SkipElement or Reset.
// A Text token is a whole text run and a CData token a whole CDATA section,
// for Reader and bytes input alike, up to MaxTokenSize (16 MiB unless set). With
// SplitText they arrive in pieces instead; Token.Partial marks a piece that
// the next token continues, and callers collecting a value must concatenate.
// ReadElement preserves the original raw bytes.
//
// The names a Decoder returns are strings of their own. After its first few
// names, a Decoder makes them through a small cache (4 KiB, holding at most
// 256 names of up to 256 bytes each), so that a name repeated in a document
// is allocated about once.
type Decoder struct {
	user           any // state of the the v2 package call reading from d; see export.go
	opts           xmlopts.Options
	legacy         *legacyChanges // nil unless Reader callbacks need settings snapshots
	legacyRefScan  int64          // absolute next byte to inspect before a Reader callback
	legacyRefStart int64          // absolute start of a pending named reference, or -1

	// effective rule set derived from opts at reset
	perm        bool // v1 Strict=true acceptance rules
	direct      bool // strict profile without SplitText: ReadToken may read tokens itself (readDirect)
	loose       bool // v1 Strict=false acceptance rules
	frags       bool
	raw         bool  // RawNesting: no stack, no matching
	rawDecl     bool  // RawNesting: the last start tag declares a namespace
	pendChar    error // permissive profiles: first bad-character error of the current text run
	unknownOK   bool
	illegalRef  bool // appendRef met a numeric reference to a char XML forbids
	illegalRune rune
	started     bool  // the first boundary search has run (the byte order mark is settled)
	docStart    int64 // stream offset of the document's first byte: 3 after a UTF-8 byte order mark
	refMax      int   // default profile: longest "&name;" scanned for a reference, from the longest registered entity name

	r              io.Reader
	charsetPending io.Reader // converter starts on the next operation, preserving the PI view
	buf            []byte
	ownBuf         bool // buf is the decoder's own read buffer, not the caller's input
	pos            int  // start of the next unread token
	eof            bool // r is exhausted (or buf holds the complete input)
	rerr           error
	base           int64 // stream offset of buf[0]

	// line accounting, advanced lazily
	lineNo    int   // newlines seen before buf[linePos]
	linePos   int   // buf index up to which newlines are counted
	lineStart int64 // stream offset of the start of the current line (as of linePos)
	// InputPos memo: the same counts as of buf[memoPos], so calling it after
	// every token costs O(n) in total. Valid while 0 < memoPos <= pos.
	memoPos   int
	memoNo    int
	memoStart int64

	// boundary engine and its batch of located, not yet parsed tokens
	eng     *t0Engine
	bnd     *[maxBatch]boundary // from batchPool while tokens are read; see dropBatch
	bi, bn  int
	scanPos int   // window index where the engine continues scanning
	engErr  error // deferred until the batch drains
	noMore  bool

	// current token
	kind     Kind
	tokS     int // start of the token's content range in buf
	tokE     int
	nameS    int // qname range of start/end/procinst target
	nameE    int
	colon    int
	attrs    []attrSpan
	verbatim bool
	scratch  []byte
	attrBuf  []byte
	selfEnd  bool // StartElement was <a/>: emit EndElement next
	popEnd   bool // pop stack entry before reading the next token
	rawEnd   bool // loose mismatch reports the lexical prefix, as v1 does
	partial  bool // the Text or CData token continues in the next one (SplitText)
	readFull bool // the last Read filled all the room it was offered: more input is likely
	maxAttrs int  // attributes per start tag; MaxInt if unlimited

	nameArena   []byte
	stack       []stackEntry
	bindings    []binding
	nsMap       map[string]int      // prefix -> index of its innermost binding; built when bindings > nsMapMin
	attrSet     map[string]struct{} // attribute names of the current tag when it has many attributes
	attrSetOn   bool
	expSet      map[string]string // expanded attribute names of a tag with many prefixed attributes
	defaultNS   string
	rootSeen    bool
	sawEOF      bool
	err         error
	deferredErr error
	rare        bool // err, deferredErr or charsetPending may be set; keeps ReadToken's common path to one test

	capturing bool
	capStart  int
	capBuf    []byte

	// stringCache, when set, makes the strings of names; it is only used
	// when unmarshaling and set through Internal, like the field of the same
	// name in encoding/json/jsontext.
	stringCache *[256]string

	// ownNames makes the strings of names otherwise, once uncached names
	// have been made without it: a document with more names than that
	// repeats most of them. It is kept across Reset; Release clears it.
	ownNames *stringCache
	uncached int
}

// ownNamesAfter is how many names a Decoder makes before it allocates its
// own string cache, so that a small document does not pay for one. The
// 4 KiB cache costs more than a hundred or so short name strings: on
// linux/arm64 a new Decoder reading a 1–4 KiB document with 40–110 names
// took 1.5–2.4× as long with the cache made after 32 names as after 128.
const ownNamesAfter = 128

// NewDecoder returns a Decoder reading from r. A *bytes.Buffer is decoded in
// place without copying; its contents must not change while decoding.
func NewDecoder(r io.Reader, opts ...Options) *Decoder {
	d := new(Decoder)
	d.Reset(r, opts...)
	return d
}

// NewDecoderBytes returns a Decoder over the complete input b without
// copying. b must not be modified while decoding.
func NewDecoderBytes(b []byte, opts ...Options) *Decoder {
	d := new(Decoder)
	d.ResetBytes(b, opts...)
	return d
}

// Reset discards all state and starts reading from r, reusing memory.
func (d *Decoder) Reset(r io.Reader, opts ...Options) {
	if bb, ok := r.(*bytes.Buffer); ok {
		d.ResetBytes(bb.Bytes(), opts...)
		return
	}
	d.reset(opts)
	d.r = r
	d.ownBuf = true
	d.eng.reset(d.opts.MaxTokenSize, d.perm, d.opts.SplitText)
	if n := firstBuf(r); cap(d.buf) < n {
		d.buf = make([]byte, 0, n)
	}
}

// ResetBytes is Reset for a complete in-memory input.
func (d *Decoder) ResetBytes(b []byte, opts ...Options) {
	d.reset(opts)
	d.r = nil
	d.ownBuf = false
	d.buf = b
	d.eof = true
	d.eng.reset(d.opts.MaxTokenSize, d.perm, d.opts.SplitText)
}

func (d *Decoder) reset(opts []Options) {
	keepBuf := d.buf[:0]
	if !d.ownBuf { // previous buffer was caller-owned
		keepBuf = nil
	}
	*d = Decoder{
		opts:        xmlopts.Join(opts...),
		buf:         keepBuf,
		attrs:       d.attrs[:0],
		scratch:     d.scratch[:0],
		attrBuf:     d.attrBuf[:0],
		nameArena:   d.nameArena[:0],
		stack:       d.stack[:0],
		bindings:    d.bindings[:0],
		capBuf:      d.capBuf[:0],
		stringCache: d.stringCache,
		ownNames:    d.ownNames,
		bnd:         d.bnd,
		lineNo:      1,
		eng:         d.eng,
	}
	if d.eng == nil {
		d.eng = new(t0Engine)
	}
	o := &d.opts
	d.loose = o.Loose
	d.raw = o.RawNesting
	d.perm = o.Permissive || o.Loose
	d.direct = !d.perm && !d.raw && o.LegacyDecoder == nil && !o.SplitText
	d.frags = o.AllowFragments
	if o.Set&xmlopts.HasAllowFragments == 0 {
		d.frags = d.perm
	}
	d.maxAttrs = o.MaxAttributes
	if d.maxAttrs <= 0 {
		d.maxAttrs = math.MaxInt
	}
	d.refMax = 66 // 64-byte names, '&' and ';'
	for name := range o.Entities {
		d.refMax = max(d.refMax, len(name)+2)
	}
	d.unknownOK = o.AllowUnknownEntit
	if o.Set&xmlopts.HasAllowUnknownEntities == 0 {
		d.unknownOK = d.loose
	}
}

// InputOffset returns the stream offset just after the last token returned.
func (d *Decoder) InputOffset() int64 { return d.base + int64(d.pos) }

// StackDepth returns the number of currently open elements. After a
// StartElement it includes that element; after an EndElement it still
// includes the element just closed until the next call.
func (d *Decoder) StackDepth() int { return len(d.stack) }

// InputPos returns the 1-based line and byte column of the current offset.
func (d *Decoder) InputPos() (line, column int) {
	return d.position(d.pos)
}

func (d *Decoder) position(at int) (line, column int) {
	if at > len(d.buf) {
		at = len(d.buf)
	}
	n, ls := d.lineAt(at)
	if at <= d.pos && at > d.memoPos {
		d.memoPos, d.memoNo, d.memoStart = at, n, ls
	}
	return n, int(d.base+int64(at)-ls) + 1
}

// lineAt returns the line number and the line's start offset at buf[at],
// counting from the memo when it is not past at.
func (d *Decoder) lineAt(at int) (int, int64) {
	n, ls, from := d.lineNo, d.lineStart, d.linePos
	if d.memoPos > 0 && d.memoPos <= at {
		n, ls, from = d.memoNo, d.memoStart, d.memoPos
	}
	if from > at {
		from = at
	}
	seg := d.buf[from:at]
	if c := bytes.Count(seg, nlByte); c > 0 {
		n += c
		ls = d.base + int64(from+bytes.LastIndexByte(seg, '\n')+1)
	}
	return n, ls
}

var nlByte = []byte{'\n'}

func (d *Decoder) syntaxErr(at int, msg string) error {
	l, c := d.position(at)
	return &SyntaxError{Offset: d.base + int64(at), Line: l, Column: c, Msg: msg}
}

// fill reads more input, preserving buf[pos:] (and a capture in progress).
// It reports whether any new bytes arrived.
func (d *Decoder) fill() bool {
	if d.eof {
		return false
	}
	if d.pos > 0 {
		// Drop consumed bytes; account newlines first.
		d.lineNo, d.lineStart = d.lineAt(d.pos)
		d.memoPos = 0
		if d.capturing {
			d.capBuf = append(d.capBuf, d.buf[d.capStart:d.pos]...)
			d.capStart = d.pos
		}
		n := copy(d.buf, d.buf[d.pos:])
		d.buf = d.buf[:n]
		d.base += int64(d.pos)
		d.scanPos -= d.pos
		if d.capturing {
			d.capStart = 0
		}
		d.pos, d.linePos = 0, 0
	}
	if c := cap(d.buf); c-len(d.buf) < lowRoom(c) || d.readFull && c < minRead {
		low := c-len(d.buf) < lowRoom(c)
		// A buffer that the last read filled completely belongs to a document
		// that is not tiny: it goes straight to a window. A buffer that merely
		// ran short of room (a large token pending) doubles; from minRead on it
		// grows by a window on top of the doubling.
		want := minRead
		switch {
		case c >= minRead:
			want = 2*c + minRead
		case !d.readFull:
			want = min(2*c, minRead)
		}
		if l, ok := d.r.(interface{ Len() int }); ok {
			// A reader that knows its remaining length only lowers the cap on
			// the doubling; a wrong (small) answer still leaves lowRoom of room.
			if n := l.Len(); n < want {
				want = min(want, max(len(d.buf)+n+1, len(d.buf)+lowRoom(want)))
			}
		}
		// make, unlike append-style growth (slices.Grow), does not clear fresh
		// memory from the OS, so the unused tail stays out of the resident
		// set. That is what keeps maxrss low while one big text is buffered.
		if low || want > c { // the ramp alone never shrinks the buffer
			nb := make([]byte, len(d.buf), want)
			copy(nb, d.buf)
			d.buf = nb
		}
	}
	for tries := 0; tries < 100; tries++ {
		var settings legacySetting
		if d.opts.LegacyDecoder != nil {
			settings = d.beforeLegacyRead()
		}
		room := cap(d.buf) - len(d.buf)
		n, err := d.r.Read(d.buf[len(d.buf):cap(d.buf)])
		d.readFull = n == room
		if d.opts.LegacyDecoder != nil {
			d.afterLegacyRead(settings, d.base+int64(len(d.buf)))
		}
		d.buf = d.buf[:len(d.buf)+n]
		if err != nil {
			d.eof = true
			if err != io.EOF {
				d.rerr = err
			}
			return n > 0
		}
		if n > 0 {
			return true
		}
	}
	d.eof = true
	d.rerr = io.ErrNoProgress
	return false
}

func (d *Decoder) fail(err error) (Token, error) {
	d.rare = true
	if d.opts.LegacyDecoder != nil {
		if se, ok := err.(*SyntaxError); ok && se.Offset >= d.base+int64(d.pos) {
			d.pos = min(int(se.Offset-d.base), len(d.buf))
		}
	}
	if d.err == nil {
		d.err = err
	}
	d.kind = Invalid
	d.dropBatch()
	return Token{}, d.err
}

// batchPool holds boundary batches. A batch holds only offsets into the
// Decoder's own buffer, so it can go back to the pool as soon as the Decoder
// can read no more tokens, which spares a Decoder that is read to its end
// (the usual case of one made by NewDecoder) its largest allocation.
var batchPool = sync.Pool{New: func() any { return new([maxBatch]boundary) }}

// dropBatch returns the boundary batch to batchPool. It is called once d has
// a final error or EOF, after which nothing reads the batch until a Reset.
func (d *Decoder) dropBatch() {
	if d.bnd != nil {
		batchPool.Put(d.bnd)
		d.bnd, d.bi, d.bn = nil, 0, 0
	}
}

// internBytes returns b as a string, from the string cache when one is set.
func (d *Decoder) internBytes(b []byte) string {
	c := d.stringCache
	if c == nil {
		if c = d.ownNames; c == nil {
			c = d.makeOwnNames()
		}
	}
	return makeString(c, b)
}

// makeOwnNames returns d.ownNames, allocating it once d has made
// ownNamesAfter names without a cache, or nil before then.
func (d *Decoder) makeOwnNames() *stringCache {
	if d.uncached < ownNamesAfter {
		d.uncached++
		return nil
	}
	d.ownNames = new(stringCache)
	return d.ownNames
}

// ensureBoundary makes sure the next token's boundary is available. It
// reports done when the input has no more tokens.
func (d *Decoder) ensureBoundary() (done bool, err error) {
	if !d.started {
		d.start()
	}
	for {
		if d.bi < d.bn {
			return false, nil
		}
		if d.engErr != nil {
			return false, d.engErr
		}
		if d.noMore {
			return true, nil
		}
		if d.bnd == nil {
			d.bnd = batchPool.Get().(*[maxBatch]boundary)
		}
		n, need, eerr := d.eng.next(d.buf, d.scanPos, d.eof, d.bnd[:])
		d.bi, d.bn = 0, n
		if n > 0 {
			d.scanPos = d.bnd[n-1].end
		}
		if eerr != nil {
			if eerr.msg == "" {
				d.engErr = d.truncated()
			} else {
				d.engErr = d.syntaxErr(eerr.at, eerr.msg)
			}
			continue
		}
		if n > 0 {
			continue
		}
		if need {
			d.fill() // sets d.eof when the input is exhausted
			continue
		}
		d.noMore = true
	}
}

// start runs once before the first token is located.
func (d *Decoder) start() {
	d.started = true
	if !d.perm {
		d.skipBOM()
	}
}

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// skipBOM steps over a UTF-8 byte order mark at the start of the stream. It is
// an encoding signature, not character data: the default profile reports no
// token for it, and the XML declaration may follow it (docStart). The
// permissive profiles leave it in the text, as encoding/xml does.
func (d *Decoder) skipBOM() {
	for len(d.buf) < 3 && bytes.HasPrefix(utf8BOM, d.buf) && d.fill() {
	}
	if bytes.HasPrefix(d.buf, utf8BOM) {
		d.pos, d.scanPos, d.docStart = 3, 3, 3
	}
}

// PeekKind returns the kind of the next token without consuming it. At end
// of input or on error it returns EOF or Invalid.
func (d *Decoder) PeekKind() Kind {
	if d.err != nil {
		return Invalid
	}
	if d.deferredErr != nil {
		return Invalid
	}
	if d.charsetPending != nil {
		d.applyCharset()
	}
	if d.selfEnd {
		return EndElement
	}
	if d.popEnd {
		d.popTop()
	}
	done, err := d.ensureBoundary()
	if err != nil {
		return Invalid
	}
	if done {
		return EOF
	}
	if d.loose && d.shouldAutoClose(d.bnd[d.bi]) {
		return EndElement
	}
	return d.bnd[d.bi].kind
}

func (d *Decoder) popTop() {
	e := &d.stack[len(d.stack)-1]
	d.stack = d.stack[:len(d.stack)-1]
	d.nameArena = d.nameArena[:e.nameOff]
	if len(d.bindings) > e.nsMark {
		d.truncBindings(e.nsMark)
	}
	if n := len(d.stack); n > 0 {
		d.defaultNS = d.stack[n-1].defaultNS
	} else {
		d.defaultNS = ""
	}
	d.popEnd = false
}

// ReadToken returns the next token. At end of input it returns an EOF token
// and io.EOF. After an error, every later call returns that error.
func (d *Decoder) ReadToken() (Token, error) {
	if d.rare {
		if d.err != nil {
			return Token{}, d.err
		}
		if d.deferredErr != nil {
			return d.fail(d.deferredErr)
		}
		if d.charsetPending != nil {
			d.applyCharset()
		}
	}
	d.rawEnd = false
	if d.popEnd {
		d.popTop()
	}
	if d.selfEnd {
		d.selfEnd = false
		d.popEnd = !d.raw
		d.kind = EndElement
		d.attrs = d.attrs[:0]
		return Token{d: d, kind: EndElement}, nil
	}
	if d.opts.LegacyDecoder != nil {
		d.beginLegacy()
	}
	if d.bi >= d.bn && d.direct {
		if !d.started {
			d.start()
		}
		if ok, err := d.readDirect(); ok {
			if err != nil {
				return d.fail(err)
			}
			d.pos = d.scanPos
			return Token{d: d, kind: d.kind}, nil
		}
	}
	if d.bi >= d.bn {
		done, err := d.ensureBoundary()
		if err != nil {
			return d.fail(err)
		}
		if done {
			return d.atEOF()
		}
	}
	bd := d.bnd[d.bi]
	if d.loose && d.shouldAutoClose(bd) {
		d.kind, d.popEnd = EndElement, true
		d.attrs = d.attrs[:0]
		return Token{d: d, kind: EndElement}, nil
	}
	d.bi++
	var err error
	switch bd.kind {
	case StartElement:
		err = d.parseStartTag(bd)
	case EndElement:
		err = d.parseEndTag(bd)
	case Text:
		err = d.parseText(bd)
	case CData:
		err = d.parseCData(bd)
	case Comment:
		err = d.parseComment(bd)
	case ProcInst:
		err = d.parseProcInst(bd)
	default:
		err = d.parseDirective(bd)
	}
	if err != nil {
		if d.raw && bd.kind == StartElement {
			err = d.rawStartErr(err, bd)
		}
		return d.fail(err)
	}
	if !d.rawEnd {
		d.pos = bd.end
	}
	return Token{d: d, kind: d.kind}, nil
}

func (d *Decoder) truncated() error {
	if d.rerr != nil {
		return d.rerr
	}
	if d.raw && d.scanPos < len(d.buf) && d.buf[d.scanPos] == '<' && d.scanPos+1 < len(d.buf) {
		if c := d.buf[d.scanPos+1]; c != '/' && c != '?' && c != '!' {
			if err := d.v1StartTagErr(d.buf[d.scanPos:], d.scanPos, true); err != nil {
				return err
			}
		}
	}
	return d.syntaxErr(len(d.buf), "unexpected EOF")
}

func (d *Decoder) atEOF() (Token, error) {
	if d.rerr != nil {
		return d.fail(d.rerr)
	}
	if len(d.stack) > 0 {
		return d.fail(d.syntaxErr(d.pos, "unexpected EOF"))
	}
	if !d.rootSeen && !d.frags {
		return d.fail(d.syntaxErr(d.pos, "no root element"))
	}
	d.kind = EOF
	d.rare = true
	d.err = io.EOF
	d.dropBatch()
	return Token{d: d, kind: EOF}, io.EOF
}

// rootDepthCheck reports whether non-space text appears outside the root.
func (d *Decoder) rootDepthCheck(body []byte) bool {
	if len(d.stack) > 0 || d.frags {
		return false
	}
	for _, c := range body {
		if !isSpace(c) {
			return true
		}
	}
	return false
}

// classifyText validates body and reports whether it contains references
// ('&') or carriage returns, which prevent verbatim use. bracket enables the
// "]]>" check for character data.
func (d *Decoder) classifyText(body []byte, at int, bracket bool) (amp, cr bool, err error) {
	cool := 0 // index before which word skipping is paused (dense non-ASCII text)
	for i := 0; i < len(body); {
		// Skip 8 plain ASCII bytes at a time; otherwise handle a few bytes bytewise.
		end := min(i+8, len(body))
		if i >= cool && i+8 <= len(body) {
			// one word inline (dense non-ASCII text fails it, and a call would cost
			// more than it finds); the kernel takes over after a plain word
			if plainWord(binary.LittleEndian.Uint64(body[i:])) {
				i += 8
				if len(body)-i >= 16 {
					i += plainPrefix(body[i:])
				}
				continue
			}
			cool = i + 24
			end = min(i+32, len(body))
		}
		for ; i < end; i++ {
			switch textClass[body[i]] {
			case cNormal:
			case cAmp:
				amp = true
			case cCR:
				cr = true
			case cBad:
				return false, false, d.syntaxErr(d.charErrAt(at, i, len(body), bracket, true), "illegal character code "+hexRune(rune(body[i])))
			case cHigh:
				n := seqLen(body[i:])
				if n == 0 {
					return false, false, d.syntaxErr(d.charErrAt(at, i, len(body), bracket, true), badRuneMsg(body[i:]))
				}
				i += n - 1
			case cBracket:
				if bracket && i+2 < len(body) && body[i+1] == ']' && body[i+2] == '>' {
					return false, false, d.syntaxErr(d.bracketErrAt(at, i), "unescaped ]]> not in CDATA section")
				}
			}
		}
	}
	return amp, cr, nil
}

const (
	lo7  = 0x7f7f7f7f7f7f7f7f
	ones = 0x0101010101010101
)

// plainWord reports whether all 8 bytes are ASCII in [0x20,0x7f] other than
// '&' (0x26) and ']' (0x5d); tab and newline are left to the byte loop.
func plainWord(w uint64) bool {
	const hi = 0x8080808080808080
	// A byte below 0x20 does not set the high bit of (b|0x80)-0x20. A byte
	// equal to c is zero in w^c, and (b-1)&^b sets the high bit of a zero
	// byte. Once any high bit of w is set the result is false whatever the
	// other terms are, so their borrows across bytes do not matter.
	lt := ^((w | hi) - 0x2020202020202020)
	x := w ^ (ones * '&')
	y := w ^ (ones * ']')
	return (w|lt|(x-ones)&^x|(y-ones)&^y)&hi == 0
}

func hexRune(r rune) string {
	const hx = "0123456789ABCDEF"
	s := []byte("U+0000")
	for i, sh := 5, 0; i >= 2; i, sh = i-1, sh+4 {
		s[i] = hx[(r>>uint(sh))&15]
	}
	return string(s)
}

// seqLen returns the length of the valid UTF-8 sequence at the start of b
// that encodes an XML Char, or 0 if there is none.
func seqLen(b []byte) int {
	c := b[0]
	switch {
	case c < 0xC2:
		return 0
	case c < 0xE0:
		if len(b) > 1 && b[1]&0xC0 == 0x80 {
			return 2
		}
	case c < 0xF0:
		if len(b) < 3 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 {
			return 0
		}
		if c == 0xE0 && b[1] < 0xA0 || c == 0xED && b[1] >= 0xA0 || c == 0xEF && b[1] == 0xBF && b[2] >= 0xBE {
			return 0
		}
		return 3
	case c < 0xF5:
		if len(b) < 4 || b[1]&0xC0 != 0x80 || b[2]&0xC0 != 0x80 || b[3]&0xC0 != 0x80 {
			return 0
		}
		if c == 0xF0 && b[1] < 0x90 || c == 0xF4 && b[1] >= 0x90 {
			return 0
		}
		return 4
	}
	return 0
}

// badRuneMsg says why the rune at the start of b was rejected, as encoding/xml does.
func badRuneMsg(b []byte) string {
	if r, n := utf8.DecodeRune(b); !(r == utf8.RuneError && n == 1) {
		return "illegal character code " + hexRune(r)
	}
	return "invalid UTF-8"
}
