// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"io"

	"github.com/Clickin/go-experiment-xml/internal/xmlopts"
)

// Options configures a Decoder or Encoder. Obtain values with the option
// constructors; the zero value leaves every setting unset. Combine several
// with the variadic parameters of NewDecoder and Reset; later options win.
type Options = xmlopts.Option

// MaxDepth limits element nesting. n <= 0 selects the default (10000).
func MaxDepth(n int) Options {
	if n <= 0 {
		n = xmlopts.DefaultMaxDepth
	}
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasMaxDepth, MaxDepth: n})
}

// MaxTokenSize limits the raw bytes of each emitted token, including markup
// delimiters. Without this option the limit is 16 MiB; zero (or negative)
// means unlimited. A Text or CData token is a whole text run or CDATA section,
// so a node larger than the limit is an error (its message names SplitText);
// with SplitText the limit applies to each piece instead. A reference or UTF-8
// rune cannot be split to evade the limit. The limit is the same for Reader
// and bytes input.
func MaxTokenSize(n int) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasMaxTokenSize, MaxTokenSize: n})
}

// MaxAttributes limits the attributes of one start tag, namespace
// declarations (xmlns, xmlns:p) included. Without this option the limit is
// 1000; zero (or negative) means unlimited.
func MaxAttributes(n int) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasMaxAttributes, MaxAttributes: n})
}

// SplitText delivers Text and CData in pieces instead of whole nodes, so
// that a text run or CDATA section of any length is read in bounded memory.
// A piece is at most 32 KiB, or MaxTokenSize if that is smaller, of raw input.
// Token.Partial reports that the next token continues the same Text run or
// the same CDATA section (adjacent CDATA sections stay distinguishable).
// Pieces never cut a reference, a UTF-8 sequence or a CRLF, and the pieces
// of one node concatenate to the value a whole token would have had. The
// node's total length is unlimited; each piece, like every other token,
// obeys MaxTokenSize. The pieces are the same for Reader and bytes input.
// Callers that collect a value must concatenate the pieces.
func SplitText(v bool) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasSplitText, SplitText: v})
}

// AllowFragments permits any number of root elements and non-space text at
// the top level, as in an XML fragment or a stream of documents.
func AllowFragments(v bool) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasAllowFragments, AllowFragments: v})
}

// WithEntities supplies extra named entities (name without & and ;). The
// predefined lt, gt, amp, apos and quot are always available. Names must be
// valid XML Names before map lookup: XML 1.0 fifth edition by default, or
// encoding/xml's legacy tables with Permissive or Loose. Invalid names such
// as "1x" and "a b" are not replaced even when present in the map; they cause
// an error unless AllowUnknownEntities or Loose preserves the reference.
// Names may be of any length. The map is not copied and must not change while decoding.
func WithEntities(m map[string]string) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasEntities, Entities: m})
}

// AllowUnknownEntities leaves undefined or malformed references in the text
// unchanged instead of reporting an error.
func AllowUnknownEntities(v bool) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasAllowUnknownEntities, AllowUnknownEntit: v})
}

// Permissive accepts encoding/xml's Decoder.Strict = true syntax relaxations.
// Name validation uses encoding/xml's legacy Unicode tables, as does v1.
// The default profile instead uses XML 1.0 fifth-edition names. It includes:
//
//   - top level: any number of roots, non-space text and CDATA outside the
//     root, and input without a root (as AllowFragments);
//   - start tags: no whitespace is required between attributes, and
//     duplicate attribute names are kept;
//   - namespaces: a prefix without a binding is not an error; the prefix
//     itself is reported in Name.Space, as it is for an attribute prefix bound
//     to the xmlns namespace (that Space is reserved to declarations, see
//     Token.Attrs). The reserved prefixes and namespaces, names that are not
//     QNames and attributes with the same expanded name are not checked;
//   - processing instructions: the target ends at the first byte that cannot
//     be in a name, so <?a'b'?> is target a, data 'b'; the xml declaration
//     is read with encoding/xml's (looser) pseudo-attribute matching, at any
//     position and in any grammar, and may switch the character set in the
//     middle of the stream. The default profile accepts the target xml only
//     as a well-formed declaration at the very start of the document, rejects
//     every other target spelled xml in any letter case, and rejects colons
//     in targets;
//   - attribute values: tab, newline and carriage return are kept as they are
//     (the default profile turns each into a space, XML 1.0 section 3.3.3,
//     while a character reference to them keeps the character);
//   - comments, processing-instruction data and directives are not checked
//     for illegal characters or invalid UTF-8;
//   - directives: any text after "<!" is a directive, and a comment inside a
//     directive is replaced by one space in Token.Text;
//   - character references: a surrogate code point becomes U+FFFD, and a code
//     point outside the XML Char production is an error.
//
// Still rejected, as in encoding/xml: "]]>" in character data, "--" inside a
// comment, mismatched end tags, unknown entities (see AllowUnknownEntities
// or Loose), and non-UTF-8 text.
func Permissive(v bool) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasPermissive, Permissive: v})
}

// Loose accepts what encoding/xml accepts with Decoder.Strict = false, which
// is HTML-ish input. It implies Permissive and AllowUnknownEntities, and adds:
//
//   - attributes without a value (the value is the attribute's local name)
//     and unquoted values made of [A-Za-z0-9_:-] bytes;
//   - an end tag that does not match the innermost open element closes open
//     elements until one matches (each yields an EndElement); an end tag
//     matching nothing is an error after the stack has been emptied;
//   - elements named by AutoClose are closed before any token other than
//     their own end tag.
//
// Explicit AllowFragments or AllowUnknownEntities options win over the
// implied values.
func Loose(v bool) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasLoose, Loose: v})
}

// AutoClose lists element local names (compared case-insensitively) that
// Loose mode closes automatically, such as the HTML void elements. It has no
// effect without Loose.
func AutoClose(names []string) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasAutoClose, AutoClose: names})
}

// WithCharsetReader supplies a converter for non-UTF-8 XML declarations.
// The label is passed unchanged and r includes all unread buffered input.
// Callback errors are wrapped; a nil reader without an error panics, as in
// encoding/xml. UTF-8 declarations do not call the converter.
func WithCharsetReader(fn func(label string, r io.Reader) (io.Reader, error)) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasCharsetReader, CharsetReader: fn})
}

// WithIndent makes an Encoder start each element, comment, processing
// instruction and directive on a new line beginning with prefix followed by
// indent repeated once per nesting level. Content inside an element that
// contains text is left untouched so mixed content is preserved. In legacy
// output mode the indentation algorithm of encoding/xml is used instead.
func WithIndent(prefix, indent string) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasIndent, IndentPrefix: prefix, IndentUnit: indent})
}

// BufferSize sets the Encoder's output buffer threshold in bytes (default
// 4096); the buffer is written to the io.Writer once it reaches this size.
func BufferSize(n int) Options {
	return xmlopts.NewOption(xmlopts.Options{Set: xmlopts.HasBufferSize, BufferSize: n})
}
