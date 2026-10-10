package oidclogin

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"
)

// decodeJSON validates the whole message, including unknown fields. Duplicate
// and case-ambiguous object keys, invalid UTF-8, excess depth/work and trailing
// documents are rejected before any library can interpret security claims.
func decodeJSON(ctx context.Context, data []byte, dst any) error {
	if len(data) == 0 || len(data) > MaxJSONBytes || !utf8.Valid(data) {
		return ErrResponse
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	work := 0
	var read func(int) error
	read = func(depth int) error {
		if ctx.Err() != nil {
			return ErrInterrupted
		}
		work++
		if depth > 16 || work > 8192 {
			return ErrResponse
		}
		v, e := d.Token()
		if e != nil {
			return ErrResponse
		}
		mark, ok := v.(json.Delim)
		if !ok {
			return nil
		}
		switch mark {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				if e != nil {
					return ErrResponse
				}
				key, ok := k.(string)
				if !ok || len(key) > 256 {
					return ErrResponse
				}
				key = strings.ToLower(key)
				if seen[key] {
					return ErrResponse
				}
				seen[key] = true
				if e = read(depth + 1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return ErrResponse
			}
		case '[':
			for d.More() {
				if e := read(depth + 1); e != nil {
					return e
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return ErrResponse
			}
		default:
			return ErrResponse
		}
		return nil
	}
	if e := read(0); e != nil {
		return e
	}
	if _, e := d.Token(); e != io.EOF {
		return ErrResponse
	}
	if e := json.Unmarshal(data, dst); e != nil {
		return ErrResponse
	}
	return nil
}
