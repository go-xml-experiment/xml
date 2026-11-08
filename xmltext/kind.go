// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

// Kind identifies the type of a Token.
type Kind byte

const (
	Invalid      Kind = iota // zero value, no token
	StartElement             // <a x="1">; <a/> yields StartElement then EndElement
	EndElement               // </a>
	Text                     // character data with references resolved
	CData                    // <![CDATA[ ... ]]>
	Comment                  // <!-- ... -->
	ProcInst                 // <?target data?>
	Directive                // <!DOCTYPE ...>
	EOF                      // end of input
)

var kindNames = [...]string{"Invalid", "StartElement", "EndElement", "Text", "CData", "Comment", "ProcInst", "Directive", "EOF"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "Kind(?)"
}
