// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xml

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
)

// Fixed token/error/position traces captured from the original stdlib decoder
// before removing the PoC's legacy copy. Large source traces use SHA-256.
var fixedResults = func() map[string][][]string {
	data, err := os.ReadFile("testdata/v2_compatibility.json")
	if err != nil {
		panic(err)
	}
	var out map[string][][]string
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}()
var fixedCallIndex = map[string]int{}

func fixedValue(group, key string, index int) []string {
	digest := fmt.Sprintf("%x", sha256.Sum256([]byte(group+"\x00"+key)))
	values, ok := fixedResults[digest]
	if !ok || index >= len(values) {
		panic(fmt.Sprintf("missing fixed expectation %s %q call %d", group, key, index))
	}
	return values[index]
}
func traceDigest(trace []string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprint(trace))))
}
