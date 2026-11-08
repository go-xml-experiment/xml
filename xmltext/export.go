// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import "github.com/Clickin/go-experiment-xml/internal/xmlopts"

// Internal is for internal use only.
// This is exempt from the Go compatibility agreement.
var Internal exporter

type exporter struct{}

// Export exposes internal functionality of the "xmltext" package
// to encoding/xml, like jsontext.Internal does for encoding/json.
func (exporter) Export(p *xmlopts.NotForPublicUse) export {
	if p != &xmlopts.AllowInternalUse {
		panic("unauthorized call to Export")
	}
	return export{}
}

type export struct{}

// StringCache returns the string cache of d, which encoding/xml sets while
// it unmarshals and clears afterwards.
func (export) StringCache(d *Decoder) **[256]string { return &d.stringCache }

// EncoderState and DecoderState return a slot that the v2 package uses to
// find its options and state from an Encoder or Decoder passed to a
// MarshalerTo or UnmarshalerFrom method, as jsontext keeps json.Options
// in its coders. An Encoder keeps it across Reset, so that a pooled
// encoding/xml Encoder reuses its state; a Decoder's Reset clears it.
func (export) EncoderState(e *Encoder) *any { return &e.user }
func (export) DecoderState(d *Decoder) *any { return &d.user }

// OpenDepth returns the number of elements open after the last token
// returned by d: unlike StackDepth, an element whose end tag was just
// returned no longer counts.
func (export) OpenDepth(d *Decoder) int {
	if d.popEnd {
		return len(d.stack) - 1
	}
	return len(d.stack)
}

// RawDecl reports, for a Decoder with RawNesting, whether the last start
// element it returned has a namespace declaration among its attributes.
func (export) RawDecl(d *Decoder) bool { return d.rawDecl }

// StartToken returns the start element token d returned last, which must
// be d's current token: encoding/xml reads its attributes later than
// ReadToken without keeping the Token.
func (export) StartToken(d *Decoder) Token { return Token{d: d, kind: StartElement} }

// PreferDefaultNamespace asks e to write the pending start tag, whose
// element has a namespace, in the default namespace when the element's
// namespace has no prefix in scope and no default namespace is in scope,
// instead of generating a prefix. the v2 package uses it for the elements
// of a qualified struct, whose unqualified children share their namespace,
// so that they need no xmlns="".
func (export) PreferDefaultNamespace(e *Encoder) {
	if e.inStart && !e.eager {
		e.tdefNS = true
	}
}

// Capturing reports whether d has a capture active; see StartCapture.
func (export) Capturing(d *Decoder) bool { return d.capturing }
