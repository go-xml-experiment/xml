// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"testing"
)

// Fixed expectations captured from the original stdlib decoder before removing
// the PoC's legacy copy. They include accepted tokens and rejected token prefixes.
var expectedResults = func() map[string]struct {
	Tokens []struct {
		Kind  Kind
		Name  Name
		Attrs []Attr
		Text  []byte
	}
	Error string
} {
	data, err := os.ReadFile("testdata/compatibility.json")
	if err != nil {
		panic(err)
	}
	var results map[string]struct {
		Tokens []struct {
			Kind  Kind
			Name  Name
			Attrs []Attr
			Text  []byte
		}
		Error string
	}
	if err := json.Unmarshal(data, &results); err != nil {
		panic(err)
	}
	return results
}()

func fixedTokens(in string, c compatibilityCfg) ([]tok, error) {
	return lookupExpected(fmt.Sprint(c), in)
}
func expectedTokens(t *testing.T, in string) ([]tok, error) {
	return lookupExpected("std", in)
}
func lookupExpected(profile, in string) ([]tok, error) {
	key := fmt.Sprintf("%x", sha256.Sum256([]byte(profile+"\x00"+in)))
	r, ok := expectedResults[key]
	if !ok {
		panic(fmt.Sprintf("missing fixed expectation for profile %s input %q", profile, in))
	}
	var tokens []tok
	for _, t := range r.Tokens {
		tokens = append(tokens, tok{t.Kind, t.Name, t.Attrs, string(t.Text)})
	}
	if r.Error != "" {
		return tokens, errors.New(r.Error)
	}
	return tokens, nil
}
