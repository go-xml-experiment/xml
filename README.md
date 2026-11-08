# go-experiment-xml

An experimental XML module for Go: a strict, namespace-aware streaming parser,
a v2 marshaling API, and a faster drop-in replacement for `encoding/xml`.

This is not an official Go package. A proposal to add it to the standard
library behind a `GOEXPERIMENT` flag ([golang/go#82926](https://github.com/golang/go/issues/82926))
was closed with a request to see third-party adoption first, so development
continues here, in the way `encoding/json/v2` began as
[`go-json-experiment/json`](https://github.com/go-json-experiment/json).
The API may change until a tagged v1.0.0 release.

| Import path | Package | Purpose |
|---|---|---|
| `github.com/Clickin/go-experiment-xml` | `xml` | v2 API: `Marshal`, `Unmarshal`, `MarshalWrite`, `UnmarshalRead`, options, caller-supplied marshalers |
| `github.com/Clickin/go-experiment-xml/xmltext` | `xmltext` | Streaming `Decoder` and `Encoder` without reflection |
| `github.com/Clickin/go-experiment-xml/v1` | `xml` | `encoding/xml` API implemented on `xmltext` |

```
go get github.com/Clickin/go-experiment-xml
```

Requires Go 1.25 or later.

## Replacing encoding/xml

Change the import and nothing else:

```go
import xml "github.com/Clickin/go-experiment-xml/v1"
```

The package keeps the behavior of `encoding/xml`, including its leniency. Its
types are distinct from those of `encoding/xml`, so code that shares
`xml.Name`, `xml.Marshaler` or similar types with a dependency still using the
standard library must change together with that dependency.

In the proposal's benchmark (one 16 MiB document, measured in a Go tree build
of this code), the `Token` loop ran 2.25 to 2.41 times as fast and `Unmarshal`
2.41 to 2.56 times as fast as `encoding/xml`, with 56% to 78% fewer
allocations. See the
[harnesses and raw results](https://github.com/Clickin/go-xml-v2/tree/5d8006963e1a24e7a6909ad857c75b33031a1c3a/benchmark-results).

## The v2 API

```go
package main

import (
	"fmt"

	xml "github.com/Clickin/go-experiment-xml"
)

type Assertion struct {
	XMLName struct{} `xml:"urn:saml Assertion"`
	NameID  string   `xml:"urn:saml NameID"`
}

func main() {
	var a Assertion
	in := `<Assertion xmlns="urn:saml"><NameID>alice</NameID></Assertion>`
	if err := xml.Unmarshal([]byte(in), &a); err != nil {
		panic(err)
	}
	fmt.Println(a.NameID)
}
```

The differences from `encoding/xml` that matter most:

- Input must be well-formed and namespace-valid. `encoding/xml` accepts
  undeclared prefixes, duplicate attributes and misplaced XML declarations.
- A tag without a namespace names an element in no namespace. It does not
  match the same local name in another namespace, which is what lets
  `encoding/xml` fill a field from another vocabulary (GO-2026-6909). The
  `qualified` flag on `XMLName` puts a struct's unqualified names in its
  namespace, like JAXB's `elementFormDefault = QUALIFIED`.
- A `[]Attr` field tagged `,any,attr` also receives namespace declarations, so
  `Marshal` writes the original prefixes. Without one, `Marshal` generates them.
- Round trips keep a document's meaning, not its bytes.

## Not supported

- Internal DTD subset entities. Declarations are not expanded.
- UTF-16 input.

## SIMD

On arm64 with Go 1.27 or later, `GOEXPERIMENT=simd` enables NEON text scanning.
Without it, the module uses portable code.

## Testing

`internal/legacyxml` is a frozen copy of `encoding/xml`. The `v1` tests compare
tokens, offsets, errors and output against it. `tools/ecosystem.py` runs the
test suites of public modules against `v1`, and `bench/` holds the benchmark
harnesses, including Java and Rust parsers for comparison.

## Origin and license

This code was developed in a Go tree fork and imported from
[Clickin/go-xml-v2](https://github.com/Clickin/go-xml-v2/tree/xml-v2-poc) at
`c759c45a38`. It derives from `encoding/xml` and is distributed under the same
BSD-style license. See [LICENSE](LICENSE).
