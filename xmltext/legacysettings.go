// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package xmltext

import (
	"bytes"
	"reflect"
	"unsafe"
)

// The v1 lexer makes Strict/Entity decisions as input is consumed, whereas the
// boundary engine reads a complete token first. Record only Reader-induced
// changes, then replay them at attribute/reference decisions, not per byte.
type legacySetting struct {
	loose    bool
	entities map[string]string // the user's map itself, not a copy
	id       unsafe.Pointer    // identity of entities, to see a replacement without looking inside
	empty    bool              // preserve an empty map even if a callback adds entries
}

type legacyChange struct {
	at int64
	legacySetting
}

type legacyChanges struct {
	initial legacySetting
	changes []legacyChange
	ready   bool
	refs    []legacyReference
}

// A reference already read must not change when the next Read mutates Entity.
// Store its lookup result, not the map or a copy of the replacement bytes.
type legacyReference struct {
	at    int64
	value string
	found bool
}

func (d *Decoder) beginLegacy() {
	c := d.opts.LegacyDecoder
	d.loose = !*c.Strict
	d.unknownOK = d.loose
	d.opts.Entities = *c.Entity
	d.opts.CharsetReader = *c.CharsetReader
	d.legacyRefScan, d.legacyRefStart = d.base+int64(d.pos), -1
	if d.legacy != nil {
		d.legacy.initial = legacySetting{}
		clear(d.legacy.changes)
		d.legacy.ready = false
		d.legacy.changes = d.legacy.changes[:0]
		clear(d.legacy.refs)
		d.legacy.refs = d.legacy.refs[:0]
	}
}

// snapshotLegacy runs on every Read in O(1). Map identity and emptiness preserve
// replacements and empty-to-nonempty changes; completed named references keep
// their individual lookup results in beforeLegacyRead.
func (d *Decoder) snapshotLegacy() legacySetting {
	c := d.opts.LegacyDecoder
	s := legacySetting{loose: !*c.Strict, entities: *c.Entity, empty: len(*c.Entity) == 0}
	if s.entities != nil {
		s.id = reflect.ValueOf(s.entities).UnsafePointer()
	}
	return s
}

func (d *Decoder) beforeLegacyRead() legacySetting {
	s := d.snapshotLegacy()
	d.rememberLegacyRefs(s)
	if d.legacy != nil && !d.legacy.ready {
		d.legacy.initial, d.legacy.ready = s, true
	}
	return s
}

func (d *Decoder) afterLegacyRead(before legacySetting, at int64) {
	after := d.snapshotLegacy()
	if before.loose == after.loose && before.id == after.id && before.empty == after.empty {
		return
	}
	if d.legacy == nil {
		d.legacy = new(legacyChanges)
	}
	if !d.legacy.ready {
		d.legacy.initial, d.legacy.ready = before, true
	}
	d.legacy.changes = append(d.legacy.changes, legacyChange{at: at, legacySetting: after})
}

func (d *Decoder) legacyAt(at int) {
	s := d.legacy.initial
	// Binary search for the first change after the position.
	pos := d.base + int64(at)
	lo, hi := 0, len(d.legacy.changes)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if d.legacy.changes[m].at > pos {
			hi = m
		} else {
			lo = m + 1
		}
	}
	i := lo
	if i > 0 {
		s = d.legacy.changes[i-1].legacySetting
	}
	d.loose, d.unknownOK, d.opts.Entities = s.loose, s.loose, s.entities
	if s.empty {
		d.opts.Entities = nil
	}
}

// rememberLegacyRefs observes buffered reference candidates before another
// Reader callback can change their values. Only appendRef consumes these
// records: this scan neither expands input nor decides whether XML is valid.
// Absolute offsets survive fill's buffer compaction; scanning resumes even
// inside a long name, so a byte-at-a-time Reader remains linear.
func (d *Decoder) rememberLegacyRefs(s legacySetting) {
	end := d.base + int64(len(d.buf))
	if d.legacyRefScan >= end {
		return
	}
	// These token kinds never expand references, including quoted directive data.
	if d.pos+1 < len(d.buf) && d.buf[d.pos] == '<' {
		switch d.buf[d.pos+1] {
		case '!', '?', '/':
			d.legacyRefScan, d.legacyRefStart = end, -1
			return
		}
	}
	i := int(d.legacyRefScan - d.base)
	for i < len(d.buf) {
		if d.legacyRefStart < 0 {
			n := bytes.IndexByte(d.buf[i:], '&')
			if n < 0 {
				break
			}
			i += n + 1
			d.legacyRefStart = d.base + int64(i)
		}
		for i < len(d.buf) && nameClass[d.buf[i]] != 0 {
			i++
		}
		if i == len(d.buf) {
			break
		}
		if d.buf[i] == ';' && !s.empty {
			ref := d.buf[int(d.legacyRefStart-d.base):i]
			_, builtin := predefinedRef(ref)
			if !builtin && d.validName(ref) {
				if d.legacy == nil {
					d.legacy = new(legacyChanges)
				}
				value, found := s.entities[string(ref)]
				d.legacy.refs = append(d.legacy.refs, legacyReference{
					at: d.base + int64(i), value: value, found: found,
				})
			}
		}
		d.legacyRefStart = -1
		// An invalid name may end at another '&'; inspect that byte again.
		if d.buf[i] != '&' {
			i++
		}
	}
	d.legacyRefScan = end
}

func (d *Decoder) legacyReferenceAt(at int) (string, bool, bool) {
	if d.legacy == nil || len(d.legacy.refs) == 0 {
		return "", false, false
	}
	offset := d.base + int64(at)
	refs := d.legacy.refs
	lo, hi := 0, len(refs)
	for lo < hi {
		m := int(uint(lo+hi) >> 1)
		if refs[m].at >= offset {
			hi = m
		} else {
			lo = m + 1
		}
	}
	i := lo
	if i < len(refs) && refs[i].at == offset {
		return refs[i].value, refs[i].found, true
	}
	return "", false, false
}
