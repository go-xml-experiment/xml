// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"github.com/go-xml-experiment/xml/internal/xmlopts"
	"github.com/go-xml-experiment/xml/xmltext"
)

// Options configure [Marshal], [MarshalWrite], [MarshalEncode], [Unmarshal],
// [UnmarshalRead] and [UnmarshalDecode]. The options of
// [github.com/go-xml-experiment/xml/xmltext] may be passed too and configure the underlying
// Encoder or Decoder. Later options override earlier ones.
type Options = xmlopts.Option

// JoinOptions coalesces srcs into a single Options value.
func JoinOptions(srcs ...Options) Options {
	return xmlopts.NewOption(xmlopts.Join(srcs...))
}

// WithMarshalers sets the type-specific marshal functions, which take
// precedence over the methods and default representation of their types.
func WithMarshalers(v *Marshalers) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasMarshalers, Marshalers: v})
}

// WithUnmarshalers sets the type-specific unmarshal functions, which take
// precedence over the methods and default representation of their types.
func WithUnmarshalers(v *Unmarshalers) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasUnmarshalers, Unmarshalers: v})
}

// RejectUnknownNames makes unmarshaling report an error for an attribute
// or child element that no struct field takes. Namespace declarations are
// never unknown. By default unknown names are skipped.
func RejectUnknownNames(v bool) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasRejectUnknownNames, RejectUnknownNames: v})
}

// OmitZeroFields makes marshaling omit every struct field that holds its
// zero value, as if each had the omitzero tag option.
func OmitZeroFields(v bool) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasOmitZeroFields, OmitZeroFields: v})
}

var _ = xmltext.AllowFragments // the xmltext options are Options too
