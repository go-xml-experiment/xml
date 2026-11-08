// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package xmlopts holds the option set shared by xmltext and the v2 binder.
package xmlopts

import "io"

// NotForPublicUse and AllowInternalUse guard xmltext.Internal, as their
// namesakes in encoding/json/internal guard jsontext.Internal.
type NotForPublicUse struct{ _ [1]byte }

// AllowInternalUse is passed to xmltext.Internal.Export.
var AllowInternalUse NotForPublicUse

// Option is an opaque option value. Its zero value leaves every setting unset.
type Option struct {
	value Options
}

// NewOption wraps settings for use by xmltext and the v1 adapter.
func NewOption(value Options) Option { return Option{value: value} }

// RawNesting makes a Decoder report start and end tags exactly as written,
// without matching them to each other, without a stack of open elements and
// without namespace bindings: an end tag with no start tag, mismatched tags and
// input that ends inside an element are not errors, and Token.Name is not
// available (use Token.RawName). It is the tokenizer mode encoding/xml's
// Decoder.RawToken needs, and is meant for the v1 compatibility layer, which
// does its own nesting and namespace handling.
func RawNesting(v bool) Option {
	return NewOption(Options{Set: HasRawNesting, RawNesting: v})
}

// LegacyOutput makes an Encoder produce the bytes encoding/xml's Encoder
// produces and apply its (lax) checks: <a></a> for an empty element, an
// xmlns="URI" attribute repeated on every namespaced element, attribute
// prefixes derived from the last URL segment, ", ' and > always escaped,
// invalid characters replaced by U+FFFD instead of reported, no name
// validation, and no duplicate attribute or root checks. It is the setting
// the v1 compatibility layer uses.
func LegacyOutput(v bool) Option {
	return NewOption(Options{Set: HasLegacyOutput, LegacyOutput: v})
}

// Options is a merged option set. The zero value means "all defaults".
// Each constructor in the public packages returns an opaque Option that sets
// exactly one field; Join merges them with later values winning.
type Options struct {
	Set uint32

	MaxDepth          int
	MaxTokenSize      int
	MaxAttributes     int
	SplitText         bool
	AllowFragments    bool
	Entities          map[string]string
	AllowUnknownEntit bool
	Permissive        bool
	Loose             bool
	AutoClose         []string
	CharsetReader     func(label string, r io.Reader) (io.Reader, error)

	// Encoder options.
	IndentPrefix string
	IndentUnit   string
	LegacyOutput bool
	BufferSize   int

	RawNesting bool

	// Semantic options, used by the v2 package and ignored by xmltext.
	Marshalers         any // *xml.Marshalers
	Unmarshalers       any // *xml.Unmarshalers
	RejectUnknownNames bool
	OmitZeroFields     bool

	// LegacyDecoder is reserved for the v1 adapter's mutable public fields.
	LegacyDecoder *LegacyDecoder
}

// LegacyDecoder supplies settings owned by v1. xmltext observes changes at
// token and Reader boundaries without resetting stream or capture state.
type LegacyDecoder struct {
	Strict        *bool
	Entity        *map[string]string
	CharsetReader *func(string, io.Reader) (io.Reader, error)
}

// Presence bits for Options.Set.
const (
	HasMaxDepth uint32 = 1 << iota
	HasMaxTokenSize
	HasAllowFragments
	HasEntities
	HasAllowUnknownEntities
	HasPermissive
	HasLoose
	HasAutoClose
	HasCharsetReader
	HasIndent
	HasLegacyOutput
	HasBufferSize
	HasRawNesting
	HasMaxAttributes
	HasSplitText
	HasMarshalers
	HasUnmarshalers
	HasRejectUnknownNames
	HasOmitZeroFields
)

const (
	DefaultMaxDepth      = 10000
	DefaultMaxTokenSize  = 16 << 20
	DefaultMaxAttributes = 1000
)

// Join returns the merge of opts, later options overriding earlier ones.
func Join(opts ...Option) Options {
	var out Options
	for i := range opts {
		o := &opts[i].value
		if o.Set&HasMaxDepth != 0 {
			out.MaxDepth = o.MaxDepth
		}
		if o.Set&HasMaxTokenSize != 0 {
			out.MaxTokenSize = o.MaxTokenSize
		}
		if o.Set&HasMaxAttributes != 0 {
			out.MaxAttributes = o.MaxAttributes
		}
		if o.Set&HasSplitText != 0 {
			out.SplitText = o.SplitText
		}
		if o.Set&HasAllowFragments != 0 {
			out.AllowFragments = o.AllowFragments
		}
		if o.Set&HasEntities != 0 {
			out.Entities = o.Entities
		}
		if o.Set&HasAllowUnknownEntities != 0 {
			out.AllowUnknownEntit = o.AllowUnknownEntit
		}
		if o.Set&HasPermissive != 0 {
			out.Permissive = o.Permissive
		}
		if o.Set&HasLoose != 0 {
			out.Loose = o.Loose
		}
		if o.Set&HasAutoClose != 0 {
			out.AutoClose = o.AutoClose
		}
		if o.Set&HasCharsetReader != 0 {
			out.CharsetReader = o.CharsetReader
		}
		if o.Set&HasIndent != 0 {
			out.IndentPrefix, out.IndentUnit = o.IndentPrefix, o.IndentUnit
		}
		if o.Set&HasLegacyOutput != 0 {
			out.LegacyOutput = o.LegacyOutput
		}
		if o.Set&HasBufferSize != 0 {
			out.BufferSize = o.BufferSize
		}
		if o.Set&HasRawNesting != 0 {
			out.RawNesting = o.RawNesting
		}
		if o.Set&HasMarshalers != 0 {
			out.Marshalers = o.Marshalers
		}
		if o.Set&HasUnmarshalers != 0 {
			out.Unmarshalers = o.Unmarshalers
		}
		if o.Set&HasRejectUnknownNames != 0 {
			out.RejectUnknownNames = o.RejectUnknownNames
		}
		if o.Set&HasOmitZeroFields != 0 {
			out.OmitZeroFields = o.OmitZeroFields
		}
		out.Set |= o.Set
		if o.LegacyDecoder != nil {
			out.LegacyDecoder = o.LegacyDecoder
		}
	}
	if out.Set&HasMaxDepth == 0 {
		out.MaxDepth = DefaultMaxDepth
	}
	// MaxTokenSize and MaxAttributes: unset selects the default, <= 0 is
	// explicitly unlimited (stored as 0).
	if out.Set&HasMaxTokenSize == 0 {
		out.MaxTokenSize = DefaultMaxTokenSize
	} else if out.MaxTokenSize < 0 {
		out.MaxTokenSize = 0
	}
	if out.Set&HasMaxAttributes == 0 {
		out.MaxAttributes = DefaultMaxAttributes
	} else if out.MaxAttributes < 0 {
		out.MaxAttributes = 0
	}
	return out
}
