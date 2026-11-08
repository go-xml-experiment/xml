// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/go-xml-experiment/xml/internal/xmlname"
)

// boundary is one token located in the window: buf[start:end] is the whole
// token including its delimiters. flags holds what the engine established
// while locating it (flagBare...); for a Text token with flagChecked, first
// is the offset of its first '&' or '\r', or -1, and the bytes before it are
// valid character data (see scanTextRun).
type boundary struct {
	start, end int
	first      int32
	kind       Kind
	flags      uint8
}

// engineError is a problem found while locating token boundaries. at is an
// index into the window; a zero value msg means "input ended inside a token".
type engineError struct {
	at  int
	msg string
}

const (
	maxBatch   = 32
	textWindow = 32 << 10
)

// scanState lets a boundary search resume after a refill without rescanning.
// Offsets are relative to the start of the token.
type scanState struct {
	from       int
	depth      int
	special    int // text: 1 + offset of the first '&' or '\r' found, or 0
	quote      byte
	inComment  bool
	heldEntity bool // a reference beginning at byte zero cannot be split
	unchecked  bool // text: scanTextRun found a problem; only '<' is searched for
}

// t0Engine locates token boundaries; the common layer (range parsers,
// names, namespaces, positions) never looks for delimiters itself. next
// scans window[pos:] and records up to len(out) complete tokens, returning
// how many. needInput reports that a token is incomplete and more input
// could complete it; the engine keeps resume state (scanState) for the
// token that starts at the pos of the next call. When final is set the
// window is all the input there will be. err describes a malformed or
// truncated token located after the n recorded ones. Text runs are located
// and checked by scanTextRun, the scanner readDirect uses too.
type t0Engine struct {
	dirty      bool // st may be non-zero
	st         scanState
	maxToken   int
	textLimit  int  // SplitText: bytes per Text/CData piece; 0 delivers whole nodes
	cdCont     bool // the previous token cut a CDATA section; the next one continues it
	partial    bool // the token just located is cut short of its node
	permissive bool
	badMsg     string
	badOff     int // offset into the token at which the permissive lexer reports badMsg
}

func (e *t0Engine) reset(maxToken int, permissive, split bool) {
	limit := 0
	if split {
		limit = textWindow
		if maxToken > 0 {
			limit = min(limit, maxToken)
		}
	}
	*e = t0Engine{maxToken: maxToken, textLimit: limit, permissive: permissive}
}

func (e *t0Engine) next(w []byte, pos int, final bool, out []boundary) (int, bool, *engineError) {
	n := 0
	for n < len(out) {
		if pos >= len(w) {
			return n, !final, nil
		}
		var kind Kind
		var end, res int
		var flags uint8
		first := int32(-1)
		b := w[pos:]
		cont := e.cdCont
		switch {
		case cont:
			kind, end, res = e.cdata(b, 0)
		case b[0] != '<':
			kind = Text
			if len(b) > 1 && b[1] == '<' && !e.dirty {
				end, res = 1, scanOK
			} else {
				end, res, flags, first = e.text(b, final)
			}
		case len(b) > 2 && b[1] == '/' && !e.dirty:
			if len(b) > 3 && b[2] != '>' && b[3] == '>' {
				kind, end, res = EndElement, 4, scanOK
			} else if j := bytes.IndexByte(b[2:], '>'); j >= 0 {
				kind, end, res = EndElement, j+3, scanOK
			} else {
				kind, end, res = e.token(b, final)
			}
		case len(b) > 1 && b[0] == '<' && b[1] != '/' && b[1] != '?' && b[1] != '!' && !e.dirty:
			if len(b) > 2 && b[2] == '>' {
				kind, end, res = StartElement, 3, scanOK
				if nameClass[b[1]] == 1 && b[1] != ':' {
					flags = flagBare
				}
			} else if len(b) > 3 && b[2] == '/' && b[3] == '>' {
				kind, end, res = StartElement, 4, scanOK
				if nameClass[b[1]] == 1 && b[1] != ':' {
					flags = flagBare
				}
			} else {
				kind, end, res = e.startTag(b)
				if res == scanOK && bareTag(b[:end]) {
					flags = flagBare
				}
			}
		default:
			kind, end, res = e.token(b, final)
		}
		switch res {
		case scanBad:
			off := e.badOff
			e.badOff = 0
			return n, false, &engineError{at: pos + off, msg: e.badMsg}
		case scanNeed:
			e.dirty = true
			if m := e.maxToken; m > 0 && len(w)-pos > m {
				return n, false, &engineError{at: pos, msg: e.tooBig(kind)}
			}
			if final {
				msg, off := "", len(w)-pos
				if e.permissive {
					msg, off = truncatedMsg(kind, w[pos:])
				}
				return n, false, &engineError{at: pos + off, msg: msg}
			}
			return n, true, nil
		}
		if m := e.maxToken; m > 0 && end > m {
			return n, false, &engineError{at: pos, msg: e.tooBig(kind)}
		}
		if e.partial {
			flags |= flagPartial
			e.partial = false
		}
		if kind == CData && !cont {
			flags |= flagCDHead
		}
		out[n] = boundary{start: pos, end: pos + end, first: first, kind: kind, flags: flags}
		n++
		pos += end
		if e.dirty {
			e.st = scanState{}
			e.dirty = false
		}
	}
	return n, false, nil
}

// text locates the end of a text run and checks it with scanTextRun. For a
// checked run it returns flagChecked and the offset of the first '&' or
// '\r', or -1. A run with a problem is only located; parseText then reports
// the problem.
func (e *t0Engine) text(b []byte, final bool) (end, res int, flags uint8, first int32) {
	if e.textLimit > 0 {
		end, res = e.splitText(b, final)
		return end, res, 0, -1
	}
	st := &e.st
	if !st.unchecked {
		n, f, r := scanTextRun(b, st.from, st.special-1)
		switch r {
		case textDone:
			return n, scanOK, checkedFlag(f), int32(f)
		case textNeed:
			if final {
				// the run ends with the input; it is checked if nothing is left
				// unscanned at its end (an incomplete sequence or "]")
				if n < len(b) {
					return len(b), scanOK, 0, -1
				}
				return len(b), scanOK, checkedFlag(f), int32(f)
			}
			e.dirty = true
			st.from, st.special = n, f+1
			return 0, scanNeed, 0, -1
		}
		e.dirty = true
		st.unchecked, st.from = true, n
	}
	if j := bytes.IndexByte(b[st.from:], '<'); j >= 0 {
		return st.from + j, scanOK, 0, -1
	}
	if final {
		return len(b), scanOK, 0, -1
	}
	st.from = len(b)
	return 0, scanNeed, 0, -1
}

// checkedFlag is flagChecked for a run checked up to first, unless first
// does not fit in boundary.first.
func checkedFlag(first int) uint8 {
	if first > math.MaxInt32 {
		return 0
	}
	return flagChecked
}

// splitText locates the end of a text piece for SplitText: a run longer than
// textLimit is cut at a safe point and marked partial; a reference longer
// than the limit stays whole. MaxTokenSize bounds that unsplittable
// reference when configured. The pieces are checked by parseText.
func (e *t0Engine) splitText(b []byte, final bool) (int, int) {
	st := &e.st
	if st.heldEntity {
		from := st.from
		lt := bytes.IndexByte(b[from:], '<')
		semi := bytes.IndexByte(b[from:], ';')
		if lt >= 0 && (semi < 0 || lt < semi) {
			return from + lt, scanOK
		}
		if semi >= 0 {
			end := from + semi + 1
			if end == len(b) && !final {
				return 0, scanNeed // whether the run goes on depends on the next byte
			}
			e.partial = end < len(b) && b[end] != '<'
			return end, scanOK
		}
		if final {
			return len(b), scanOK
		}
		st.from = len(b)
		return 0, scanNeed
	}
	limit := e.textLimit
	until := len(b)
	if limit > 0 {
		until = min(until, limit+1)
	}
	if j := bytes.IndexByte(b[st.from:until], '<'); j >= 0 {
		return st.from + j, scanOK
	}
	if limit <= 0 || len(b) <= limit {
		if final {
			return len(b), scanOK
		}
		st.from = len(b)
		return 0, scanNeed
	}
	end := safeEnd(b[:limit], true)
	if end > 0 {
		e.partial = true // b[end] exists and is not '<'
		return end, scanOK
	}
	if b[0] == '&' {
		e.dirty = true
		st.heldEntity, st.from = true, limit
		return e.splitText(b, final)
	}
	return 0, scanNeed
}

// cdata locates the end of a CDATA piece of b: head is the length of the
// opening delimiter b starts with, or 0 for the continuation of a cut section.
// With SplitText a piece is cut at a safe point once the section would pass
// textLimit; "]]>" is only recognized inside that limit, and the cut keeps
// the trailing ']' bytes, so the terminator is always found at the start of the
// next piece or inside one piece.
func (e *t0Engine) cdata(b []byte, head int) (Kind, int, int) {
	st := &e.st
	limit := e.textLimit
	until := len(b)
	if limit > 0 {
		until = min(until, limit)
	}
	from := max(st.from, head)
	if from < until { // a limit below the delimiter's length leaves nothing to search
		if j := bytes.Index(b[from:until], bracketsGT); j >= 0 {
			e.cdCont = false
			return CData, from + j + 3, scanOK
		}
	}
	if limit > 0 && len(b) > limit {
		if end := safeEnd(b[:limit], false); end > head {
			e.cdCont, e.partial = true, true
			return CData, end, scanOK
		}
	}
	st.from = max(head, len(b)-2)
	return CData, 0, scanNeed
}

func (e *t0Engine) tooBig(kind Kind) string {
	if e.textLimit == 0 && (kind == Text || kind == CData) {
		return "token exceeds MaxTokenSize; use the SplitText option to read text or CDATA larger than the limit"
	}
	return "token exceeds MaxTokenSize"
}

// safeEnd is where the piece b[:len(b)] of a text run (refs) or CDATA section
// may end. It retains incomplete references (text only), UTF-8 sequences and
// CRLF, and the last two ']' bytes, so validation of a following "]]>" never
// depends on a previous token.
func safeEnd(b []byte, refs bool) int {
	end := len(b)
	start := end - 1
	for start > 0 && !utf8.RuneStart(b[start]) {
		start--
	}
	if !utf8.FullRune(b[start:end]) {
		end = start
	}
	for n := 0; n < 2 && end > 0 && b[end-1] == ']'; n++ {
		end--
	}
	if end > 0 && b[end-1] == '\r' {
		end--
	}
	if refs {
		if amp := lastIndexByte(b[:end], '&'); amp >= 0 && bytes.IndexByte(b[amp:end], ';') < 0 {
			end = amp
		}
	}
	return end
}

// lastIndexByte is bytes.LastIndexByte, which is a scalar loop, made to skip
// blocks without c using the vectorized bytes.IndexByte.
func lastIndexByte(b []byte, c byte) int {
	const block = 256
	for end := len(b); end > 0; {
		start := max(end-block, 0)
		if bytes.IndexByte(b[start:end], c) >= 0 {
			return start + bytes.LastIndexByte(b[start:end], c)
		}
		end = start
	}
	return -1
}

const (
	scanOK = iota
	scanNeed
	scanBad
)

// token locates the end of the token at the start of b.
func (e *t0Engine) token(b []byte, final bool) (Kind, int, int) {
	st := &e.st
	if b[0] != '<' {
		end, res, _, _ := e.text(b, final)
		return Text, end, res
	}
	if len(b) < 2 {
		return Invalid, 0, scanNeed
	}
	switch b[1] {
	case '/':
		j := bytes.IndexByte(b[max(st.from, 2):], '>')
		if j < 0 {
			st.from = len(b)
			return EndElement, 0, scanNeed
		}
		return EndElement, max(st.from, 2) + j + 1, scanOK
	case '?':
		from := max(st.from, 2)
		j := bytes.Index(b[from:], qmGT)
		if j < 0 {
			st.from = max(2, len(b)-1)
			return ProcInst, 0, scanNeed
		}
		return ProcInst, from + j + 2, scanOK
	case '!':
		if len(b) < 4 && !final && (len(b) < 3 || b[2] == '-' || b[2] == '[') {
			return Invalid, 0, scanNeed
		}
		if len(b) >= 4 && b[2] == '-' && b[3] == '-' {
			from := max(st.from, 4)
			j := bytes.Index(b[from:], dashDashGT)
			if j < 0 {
				st.from = max(4, len(b)-2)
				return Comment, 0, scanNeed
			}
			return Comment, from + j + 3, scanOK
		}
		if e.permissive && len(b) >= 3 && b[2] == '-' {
			if len(b) == 3 {
				return Invalid, 0, scanNeed // "<!-" cut off by the end of input
			}
			e.badMsg, e.badOff = "invalid sequence <!- not part of <!--", 4 // the byte after "<!-" was read
			return Invalid, 0, scanBad
		}
		if len(b) >= 3 && b[2] == '[' {
			if len(b) < 9 && !final && strings.HasPrefix("<![CDATA[", string(b)) {
				return Invalid, 0, scanNeed
			}
			if len(b) >= 9 && string(b[:9]) == "<![CDATA[" {
				return e.cdata(b, 9)
			}
			if e.permissive {
				if final && strings.HasPrefix("<![CDATA[", string(b)) {
					return Invalid, 0, scanNeed // a prefix of "<![CDATA[" cut off by the end of input
				}
				e.badMsg, e.badOff = "invalid <![ sequence", 4
				for j := 0; j < len("CDATA[") && 3+j < len(b); j++ {
					if b[3+j] != "CDATA["[j] {
						e.badOff = 3 + j + 1 // the first byte that is not part of "CDATA[" was read
						break
					}
				}
				return Invalid, 0, scanBad
			}
		}
		return e.directive(b, final)
	}
	return e.startTag(b)
}

var (
	qmGT       = []byte("?>")
	dashDashGT = []byte("-->")
	bracketsGT = []byte("]]>")
)

func (e *t0Engine) startTag(b []byte) (Kind, int, int) {
	sc := &e.st
	i := max(sc.from, 1)
	for i < len(b) {
		if sc.quote != 0 {
			// attribute values are usually short: scan a few bytes inline
			// before paying for the indexByte call
			j := -1
			for k, lim := i, min(i+16, len(b)); k < lim; k++ {
				if b[k] == sc.quote {
					j = k - i
					break
				}
			}
			if j < 0 && i+16 < len(b) {
				if j = bytes.IndexByte(b[i+16:], sc.quote); j >= 0 {
					j += 16
				}
			}
			if j < 0 {
				i = len(b)
				break
			}
			i += j + 1
			sc.quote = 0
			continue
		}
		for i < len(b) && !tagStop[b[i]] {
			i++
		}
		if i == len(b) {
			break
		}
		if b[i] == '>' {
			return StartElement, i + 1, scanOK
		}
		sc.quote = b[i]
		e.dirty = true
		i++
	}
	sc.from = i
	return StartElement, 0, scanNeed
}

func (e *t0Engine) directive(b []byte, final bool) (Kind, int, int) {
	e.dirty = true
	sc := &e.st
	i := max(sc.from, 2)
	if e.permissive {
		i = max(sc.from, 3) // v1 stores the first directive byte without interpreting it
	}
	for i < len(b) {
		if sc.inComment {
			j := bytes.Index(b[i:], dashDashGT)
			if j < 0 {
				sc.from = max(i, len(b)-2)
				return Directive, 0, scanNeed
			}
			i += j + 3
			sc.inComment = false
			continue
		}
		c := b[i]
		if sc.quote != 0 {
			j := bytes.IndexByte(b[i:], sc.quote)
			if j < 0 {
				sc.from = len(b)
				return Directive, 0, scanNeed
			}
			i += j + 1
			sc.quote = 0
			continue
		}
		switch c {
		case '"', '\'':
			sc.quote = c
			i++
		case '<':
			if len(b)-i < 4 && !final {
				sc.from = i
				return Directive, 0, scanNeed
			}
			if len(b)-i >= 4 && b[i+1] == '!' && b[i+2] == '-' && b[i+3] == '-' {
				sc.inComment = true
				i += 4
				continue
			}
			sc.depth++
			i++
		case '>':
			if sc.depth == 0 {
				return Directive, i + 1, scanOK
			}
			sc.depth--
			i++
		default:
			i++
		}
	}
	sc.from = i
	return Directive, 0, scanNeed
}

// flagBare marks a start tag that is just "<name>" or "<name/>" with an
// ASCII name free of ':' , so the parser can skip attribute and prefix work.
// The engine sets it only after checking the name, so the parser may trust it.
const flagBare = 1

// Text and CData boundaries. flagPartial: the next token continues this node.
// flagCDHead: the token begins with "<![CDATA[". A CData token that is not
// partial ends with "]]>".
const (
	flagPartial = 2
	flagCDHead  = 4
	flagChecked = 8
)

// bareTag reports whether tag (a complete start tag including '<' and '>')
// is "<name>" or "<name/>" with a plain ASCII, colon-free name.
func bareTag(tag []byte) bool {
	n := len(tag) - 1 // index of '>'
	if tag[n-1] == '/' {
		n--
	}
	if nameClass[tag[n-1]] == 0 || nameClass[tag[n-1]] == 3 { // quote, space, '=': has attributes
		return false
	}
	if n < 2 || nameClass[tag[1]] != 1 || tag[1] == ':' {
		return false
	}
	for _, c := range tag[2:n] {
		if cl := nameClass[c]; cl == 0 || cl == 3 || c == ':' {
			return false
		}
	}
	return true
}

// truncatedMsg is what encoding/xml reports for a token that the end of input
// cuts off, with the offset into b at which its lexer noticed (that decides
// the line number). An empty message means the generic "unexpected EOF". It
// reads as far as the legacy lexer would have before running out of bytes.
// Its only caller is guarded by e.permissive, so names use the legacy tables;
// the default profile instead reports the generic unexpected EOF.
func truncatedMsg(kind Kind, b []byte) (string, int) {
	nameRun := func(from int) int {
		j := from
		for j < len(b) && (isNameByte(b[j]) || b[j] >= 0x80) {
			j++
		}
		return j
	}
	switch kind {
	case CData:
		return "unexpected EOF in CDATA section", len(b)
	case Comment:
		if off := commentViolation(b[4:]); off >= 0 {
			return `invalid sequence "--" not allowed in comments`, 4 + off
		}
	case StartElement:
		return "", len(b) // the decoder re-lexes the tag (v1StartTagErr)
	case ProcInst:
		j := nameRun(2)
		if j == 2 {
			if len(b) == 2 {
				return "", len(b)
			}
			return "expected target name after <?", 2
		}
		if j == len(b) {
			return "", len(b)
		}
		if !xmlname.Valid(b[2:j]) {
			return "invalid XML name: " + string(b[2:j]), j
		}
	case EndElement:
		j := nameRun(2)
		if j == 2 {
			if len(b) == 2 {
				return "", len(b)
			}
			return "expected element name after </", 2
		}
		if j == len(b) {
			return "", len(b)
		}
		if !xmlname.Valid(b[2:j]) {
			return "invalid XML name: " + string(b[2:j]), j
		}
		k := j
		for k < len(b) && isSpace(b[k]) {
			k++
		}
		if k < len(b) {
			_, local := rawSplit(b[2:j])
			return "invalid characters between </" + local + " and >", k + 1
		}
	}
	return "", len(b)
}

// readDirect reads the token at d.scanPos without the batch, for the common
// kinds the engine would locate in one step: text, an end tag and a start
// tag that lie whole in the buffer. Text and start tags are checked while
// they are located (see scanTextRun and readStartTag). readDirect reports
// false for anything else, and the engine then locates the token, including
// every token with a syntax error and every case that needs more input. It
// changes nothing before reporting false except state that parsing a token
// resets, and the engine's resume state for a text run that goes on past
// the buffer: the engine continues that run at d.scanPos where the scan here
// stopped.
func (d *Decoder) readDirect() (bool, error) {
	e := d.eng
	pos := d.scanPos
	b := d.buf[pos:]
	if e.dirty || e.cdCont || e.textLimit > 0 || len(b) < 2 {
		return false, nil
	}
	switch {
	case b[0] != '<':
		end, first, res := scanTextRun(b, 0, -1)
		if res != textDone || !e.fits(end) {
			if res == textNeed {
				e.dirty = true
				e.st.from, e.st.special = end, first+1
			}
			return false, nil
		}
		d.scanPos = pos + end
		d.pendChar = nil
		return true, d.finishText(boundary{start: pos, end: pos + end, kind: Text}, first)
	case b[1] == '/':
		end := 0
		if len(b) > 3 && b[2] != '>' && b[3] == '>' {
			end = 4
		} else if j := bytes.IndexByte(b[2:], '>'); j >= 0 {
			end = j + 3
		} else {
			return false, nil
		}
		if !e.fits(end) {
			return false, nil
		}
		d.scanPos = pos + end
		return true, d.parseEndTag(boundary{start: pos, end: pos + end, kind: EndElement})
	case len(b) > 2 && b[2] == '>' && nameClass[b[1]] == 1 && b[1] != ':':
		// <x>, as in t0Engine.next
		if !e.fits(3) {
			return false, nil
		}
		d.scanPos = pos + 3
		return true, d.pushBare(boundary{start: pos, end: pos + 3, kind: StartElement})
	case b[1] != '?' && b[1] != '!':
		return d.readStartTag(pos)
	}
	return false, nil
}

// fits reports whether a token of n bytes is within MaxTokenSize.
func (e *t0Engine) fits(n int) bool {
	return e.maxToken <= 0 || n <= e.maxToken
}
