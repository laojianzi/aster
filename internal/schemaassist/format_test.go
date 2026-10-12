package schemaassist

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
)

func formatProperty(t testing.TB, format any) string {
	t.Helper()
	return constraintEncode(t, map[string]any{"type": "string", "format": format})
}

func TestSchemaFormatSupportedStringValues(t *testing.T) {
	for _, tc := range []struct {
		name, format, value string
		matched             bool
	}{
		{"date-ordinary", "date", "2026-10-12", true},
		{"date-leap-four", "date", "2004-02-29", true},
		{"date-leap-four-hundred", "date", "2000-02-29", true},
		{"date-year-zero", "date", "0000-02-29", true},
		{"date-last-year", "date", "9999-12-31", true},
		{"date-name-hyphens", "d-a-t-e", "2024-02-29", true},
		{"date-century-not-leap", "date", "1900-02-29", false},
		{"date-year-not-leap", "date", "2023-02-29", false},
		{"date-short-month", "date", "2024-2-29", false},
		{"date-short-day", "date", "2024-02-9", false},
		{"date-zero-month", "date", "2024-00-01", false},
		{"date-month-overflow", "date", "2024-13-01", false},
		{"date-zero-day", "date", "2024-01-00", false},
		{"date-month-day-overflow", "date", "2024-04-31", false},
		{"date-empty", "date", "", false},
		{"date-leading-space", "date", " 2024-02-29", false},
		{"date-trailing-newline", "date", "2024-02-29\n", false},
		{"date-timestamp", "date", "2024-02-29T00:00:00Z", false},
		{"date-negative-year", "date", "-001-02-28", false},
		{"date-five-digit-year", "date", "10000-01-01", false},
		{"date-unicode-digits", "date", "２０２４-０２-２９", false},
		{"byte-full-quartet", "byte", "YWJj", true},
		{"byte-one-padding", "byte", "YWI=", true},
		{"byte-two-padding", "byte", "YQ==", true},
		{"byte-zero-data", "byte", "AAAA", true},
		{"byte-plus-slash", "byte", "+///", true},
		{"byte-multiple-quartets", "byte", "YWJjZGU=", true},
		{"byte-nonzero-two-padding-bits", "byte", "AB==", true},
		{"byte-nonzero-one-padding-bits", "byte", "AAB=", true},
		{"byte-name-hyphens", "--b-y-t-e--", "YWJj", true},
		{"byte-empty", "byte", "", false},
		{"byte-missing-two-padding", "byte", "YQ", false},
		{"byte-missing-one-padding", "byte", "YWI", false},
		{"byte-line-feed", "byte", "YWJj\n", false},
		{"byte-crlf-in-quartet", "byte", "AA\r\nAA==", false},
		{"byte-url-safe", "byte", "-___", false},
		{"byte-interior-padding", "byte", "YQ==YWJj", false},
		{"byte-excess-padding", "byte", "A===", false},
		{"byte-only-padding", "byte", "====", false},
		{"byte-leading-padding", "byte", "=AAA", false},
		{"byte-space-in-quartet", "byte", "AA A", false},
		{"byte-unicode", "byte", "船A", false},
		{"uuid-canonical", "uuid", "123e4567-e89b-12d3-a456-426614174000", true},
		{"uuid-uppercase", "uuid", "123E4567-E89B-12D3-A456-426614174000", true},
		{"uuid-no-hyphens", "uuid", "123e4567e89b12d3a456426614174000", true},
		{"uuid-mixed-hyphens", "uuid", "123e4567e89b-12d3a456-426614174000", true},
		{"uuid-nil", "uuid", "00000000-0000-0000-0000-000000000000", true},
		{"uuid-unrestricted-version-variant", "uuid", "ffffffff-ffff-ffff-ffff-ffffffffffff", true},
		{"uuid-name-hyphens", "u-u-i-d", "123e4567-e89b-12d3-a456-426614174000", true},
		{"uuid-empty", "uuid", "", false},
		{"uuid-short", "uuid", "123e4567-e89b-12d3-a456-42661417400", false},
		{"uuid-long", "uuid", "123e4567-e89b-12d3-a456-4266141740000", false},
		{"uuid-misplaced-hyphen", "uuid", "123e456-7e89b-12d3-a456-426614174000", false},
		{"uuid-extra-hyphen", "uuid", "123e4567--e89b12d3a456426614174000", false},
		{"uuid-trailing-hyphen", "uuid", "123e4567e89b12d3a456426614174000-", false},
		{"uuid-nonhex", "uuid", "123e4567-e89b-12d3-a456-42661417400g", false},
		{"uuid-braces", "uuid", "{123e4567-e89b-12d3-a456-426614174000}", false},
		{"uuid-urn", "uuid", "urn:uuid:123e4567-e89b-12d3-a456-426614174000", false},
		{"uuid-whitespace", "uuid", "123e4567e89b12d3a456426614174000 ", false},
		{"uuid-unicode-hyphen", "uuid", "123e4567−e89b12d3a456426614174000", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codes := []string{}
			if !tc.matched {
				codes = append(codes, "format")
			}
			r := constraintCheck(t, formatProperty(t, tc.format), tc.value)
			constraintWant(t, r, false, codes...)
			for _, diagnostic := range r.Diagnostics {
				if diagnostic.Pointer != "/value" || diagnostic.Message != "String does not match the supported advertised format." {
					t.Fatal("format hint lost its original pointer or fixed message")
				}
			}
		})
	}
}

func TestSchemaFormatMatchesIndependentExpectations(t *testing.T) {
	check := func(t *testing.T, d *Document, value string, matched bool) {
		t.Helper()
		r, err := d.Check(context.Background(), constraintObject(value))
		if err != nil || r.Partial || (len(r.Diagnostics) == 0) != matched ||
			(!matched && (len(r.Diagnostics) != 1 || r.Diagnostics[0].Code != "format" || r.Diagnostics[0].Pointer != "/value")) {
			t.Fatalf("independent format expectation disagreed for %q: report %+v, err %v", value, r, err)
		}
	}
	t.Run("date-gregorian-calendar", func(t *testing.T) {
		// Calendar arithmetic is independent of the production time.Parse.
		d, _ := constraintDocument(t, formatProperty(t, "date"))
		comparisons := 0
		for _, year := range []int{0, 1, 4, 100, 400, 1900, 1999, 2000, 2004, 2100, 2400, 9999} {
			for month := 0; month <= 13; month++ {
				last := 0
				if month >= 1 && month <= 12 {
					last = [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[month-1]
					if month == 2 && (year%400 == 0 || year%4 == 0 && year%100 != 0) {
						last++
					}
				}
				for day := 0; day <= 32; day++ {
					check(t, d, fmt.Sprintf("%04d-%02d-%02d", year, month, day), day >= 1 && day <= last)
					comparisons++
				}
			}
		}
		if comparisons != 5544 {
			t.Fatal("Gregorian matrix coverage changed", comparisons)
		}
		t.Logf("independent date comparisons: %d", comparisons)
	})
	t.Run("byte-encoder-and-ascii-mutations", func(t *testing.T) {
		d, _ := constraintDocument(t, formatProperty(t, "byte"))
		// The pinned server grammar is an independent regexp oracle, not the
		// production byte scan. Encoder outputs also cover every padding size.
		oracle := regexp.MustCompile(`^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{4}|[A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{2}==)$`)
		comparisons := 0
		for size := 0; size < 96; size++ {
			payload := make([]byte, size)
			for i := range payload {
				payload[i] = byte(i*37 + size)
			}
			check(t, d, base64.StdEncoding.EncodeToString(payload), size > 0)
			comparisons++
		}
		for i := 0; i < 4; i++ {
			for ch := 0; ch < 128; ch++ {
				value := []byte("QUJD")
				value[i] = byte(ch)
				check(t, d, string(value), oracle.Match(value))
				comparisons++
			}
		}
		if comparisons != 608 {
			t.Fatal("base64 matrix coverage changed", comparisons)
		}
		t.Logf("independent byte comparisons: %d", comparisons)
	})
	t.Run("uuid-all-separator-combinations-and-ascii-mutations", func(t *testing.T) {
		d, _ := constraintDocument(t, formatProperty(t, "uuid"))
		oracle := regexp.MustCompile(`^[a-fA-F0-9]{8}-?[a-fA-F0-9]{4}-?[a-fA-F0-9]{4}-?[a-fA-F0-9]{4}-?[a-fA-F0-9]{12}$`)
		comparisons := 0
		for _, payload := range []string{"123e4567e89b12d3a456426614174000", "123E4567E89B12D3A456426614174000", "00000000000000000000000000000000", "ffffffffffffffffffffffffffffffff"} {
			for mask := 0; mask < 16; mask++ {
				value := payload
				for bit, offset := range []int{20, 16, 12, 8} {
					if mask&(1<<bit) != 0 {
						value = value[:offset] + "-" + value[offset:]
					}
				}
				check(t, d, value, true)
				comparisons++
			}
		}
		for _, offset := range []int{0, 8, 13, 35} {
			for ch := 0; ch < 128; ch++ {
				value := []byte("123e4567-e89b-12d3-a456-426614174000")
				value[offset] = byte(ch)
				check(t, d, string(value), oracle.Match(value))
				comparisons++
			}
		}
		if comparisons != 576 {
			t.Fatal("UUID matrix coverage changed", comparisons)
		}
		t.Logf("independent uuid comparisons: %d", comparisons)
	})
}

func TestSchemaFormatRejectsMalformedAndInapplicable(t *testing.T) {
	for _, tc := range []struct {
		name   string
		format any
	}{
		{"null-metadata", nil}, {"number-metadata", 42}, {"boolean-metadata", true},
		{"array-metadata", []any{"PRIVATE_FORMAT"}}, {"object-metadata", map[string]any{"PRIVATE": "FORMAT"}},
		{"empty-metadata", ""}, {"hyphens-only", "---"}, {"uppercase-name", "DATE"},
		{"whitespace-name", " date "}, {"unicode-name", "ｄａｔｅ"}, {"private-format", "PRIVATE_SCHEMA_FORMAT"},
		{"unsupported-email", "email"}, {"unsupported-date-time", "date-time"},
		{"unsupported-ipv4", "ipv4"}, {"unsupported-ipv6", "ipv6"},
		{"unsupported-int32", "int32"}, {"unsupported-int64", "int64"},
		{"unsupported-float", "float"}, {"unsupported-double", "double"},
		{"unsupported-binary", "binary"}, {"unsupported-password", "password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := constraintCheck(t, formatProperty(t, tc.format), "PRIVATE_DRAFT_VALUE🚢")
			constraintWant(t, r, true)
			if strings.Contains(r.Text()+constraintEncode(t, r), "PRIVATE_") {
				t.Fatal("unsupported format report disclosed schema or input")
			}
		})
	}
	for _, tc := range []struct {
		name     string
		property map[string]any
		value    any
		partial  bool
		codes    []string
	}{
		{"absent-format", map[string]any{"type": "string"}, "anything", false, nil},
		{"valid-format-wrong-integer-type", map[string]any{"type": "string", "format": "date"}, int64(7), false, []string{"type"}},
		{"valid-format-wrong-boolean-type", map[string]any{"type": "string", "format": "byte"}, true, false, []string{"type"}},
		{"malformed-format-wrong-type", map[string]any{"type": "string", "format": false}, int64(7), true, []string{"type"}},
		{"unknown-format-wrong-type", map[string]any{"type": "string", "format": "private-format"}, false, true, []string{"type"}},
		{"nullable-null", map[string]any{"type": "string", "nullable": true, "format": "date"}, nil, false, nil},
		{"nullable-null-malformed", map[string]any{"type": "string", "nullable": true, "format": nil}, nil, true, nil},
		{"nullable-null-unknown", map[string]any{"type": "string", "nullable": true, "format": "email"}, nil, true, nil},
		{"nonnullable-null", map[string]any{"type": "string", "format": "uuid"}, nil, false, []string{"type"}},
		{"untyped-string", map[string]any{"format": "date"}, "no", false, []string{"format"}},
		{"untyped-number", map[string]any{"format": "date"}, int64(7), false, nil},
		{"boolean-irrelevant-valid-format", map[string]any{"type": "boolean", "format": "uuid"}, true, false, nil},
		{"boolean-irrelevant-malformed-format", map[string]any{"type": "boolean", "format": []any{}}, true, true, nil},
		{"numeric-format-remains-incomplete", map[string]any{"type": "integer", "format": "int32"}, int64(7), true, nil},
		{"canonical-union-string", map[string]any{"x-kubernetes-int-or-string": true, "anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}, "format": "date"}, "no", false, []string{"format"}},
		{"canonical-union-integer", map[string]any{"x-kubernetes-int-or-string": true, "anyOf": []any{map[string]any{"type": "integer"}, map[string]any{"type": "string"}}, "format": "date"}, int64(7), false, nil},
		{"enum-and-format", map[string]any{"type": "string", "enum": []any{"2024-02-29"}, "format": "date"}, "no", false, []string{"enum", "format"}},
		{"pattern-and-format", map[string]any{"type": "string", "pattern": "^2024-", "format": "date"}, "1900-02-29", false, []string{"pattern", "format"}},
		{"length-and-format", map[string]any{"type": "string", "minLength": 10, "format": "date"}, "x", false, []string{"minLength", "format"}},
		{"unknown-pattern-with-format", map[string]any{"type": "string", "pattern": ".", "format": "date"}, "no", true, []string{"format"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, constraintEncode(t, tc.property), tc.value), tc.partial, tc.codes...)
		})
	}
	t.Run("invalid-utf8-decoded-value", func(t *testing.T) {
		constraintWant(t, constraintCheck(t, formatProperty(t, "byte"), string([]byte{0xff, 'A', '=', '='})), true)
	})
	t.Run("invalid-utf8-schema-metadata", func(t *testing.T) {
		d, _ := constraintDocument(t, formatProperty(t, "date"))
		d.root["properties"].(map[string]any)["value"].(map[string]any)["format"] = string([]byte{0xff})
		r, err := d.Check(context.Background(), constraintObject("no"))
		if err != nil {
			t.Fatal(err)
		}
		constraintWant(t, r, true)
	})
	t.Run("reference-wrapper-format-remains-uninspected", func(t *testing.T) {
		d, _ := constraintDocument(t, `{"type":"string"}`)
		d.definitions["ReferencedString"] = map[string]any{"type": "string"}
		d.root["properties"].(map[string]any)["value"] = map[string]any{
			"allOf": []any{map[string]any{"$ref": "#/components/schemas/ReferencedString"}}, "format": "date",
		}
		r, err := d.Check(context.Background(), constraintObject("no"))
		if err != nil {
			t.Fatal(err)
		}
		constraintWant(t, r, true)
	})
}

func TestSchemaFormatBudgetsAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name, format, value string
		partial             bool
		codes               []string
	}{
		{"name-byte-limit", strings.Repeat("-", MaxFormatNameBytes-4) + "byte", "YWJj", false, nil},
		{"name-byte-limit-plus-one", strings.Repeat("-", MaxFormatNameBytes-3) + "byte", "YWJj", true, nil},
		{"input-byte-limit", "byte", strings.Repeat("A", MaxFormatInputBytes), false, nil},
		{"input-byte-limit-invalid", "byte", strings.Repeat("A", MaxFormatInputBytes-1) + "!", false, []string{"format"}},
		{"input-byte-limit-plus-one", "byte", strings.Repeat("A", MaxFormatInputBytes+1), true, nil},
		{"input-limit-multibyte", "date", strings.Repeat("船", MaxFormatInputBytes/3+1), true, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			constraintWant(t, constraintCheck(t, formatProperty(t, tc.format), tc.value), tc.partial, tc.codes...)
		})
	}
	t.Run("format-work-is-shared-across-values", func(t *testing.T) {
		property := constraintEncode(t, map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "byte"}})
		values := make([]any, 100)
		for i := range values {
			values[i] = strings.Repeat("A", MaxFormatInputBytes)
		}
		r := constraintCheck(t, property, values)
		constraintWant(t, r, true)
		if r.Checked < MaxWork/2 || r.Checked > MaxWork+len(values)+8 {
			t.Fatal("format scanning did not consume the shared work allowance", r.Checked)
		}
	})
	for _, prior := range []string{"work", "name-bytes", "input-bytes", "other-constraint"} {
		t.Run("prior-"+prior+"-exhaustion-does-not-invent-hint", func(t *testing.T) {
			r := Report{}
			c := constraintChecker{ctx: context.Background(), report: &r, add: func(path, code, message string) {
				r.Diagnostics = append(r.Diagnostics, Diagnostic{path, code, message})
			}}
			switch prior {
			case "work":
				r.Checked = MaxWork
			case "name-bytes":
				c.bytes = MaxConstraintBytes
			case "input-bytes":
				c.bytes = MaxConstraintBytes - len("byte")
			case "other-constraint":
				if !c.chargeWork(MaxWork-2, 0) {
					t.Fatal("could not reserve prior work")
				}
			}
			c.checkFormat(map[string]any{"format": "byte"}, "PRIVATE_DRAFT_VALUE", "/value", true)
			constraintWant(t, r, true)
		})
	}
	t.Run("cancel-before-inspection", func(t *testing.T) {
		d, _ := constraintDocument(t, formatProperty(t, "byte"))
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := d.Check(ctx, constraintObject("YWJj")); !errors.Is(err, context.Canceled) {
			t.Fatal("initial format cancellation was lost", err)
		}
	})
	t.Run("cancel-during-repeated-format-inspection", func(t *testing.T) {
		property := constraintEncode(t, map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "byte"}})
		d, _ := constraintDocument(t, property)
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: 28}
		if _, err := d.Check(ctx, constraintObject([]any{"YWJj", "YWJj", "YWJj", "YWJj"})); !errors.Is(err, context.Canceled) {
			t.Fatal("in-flight format cancellation was lost", err)
		}
	})
	t.Run("cancel-after-validator-does-not-invent-hint", func(t *testing.T) {
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		ctx := &constraintCancelAfter{Context: base, cancel: cancel, after: 4}
		r := Report{}
		c := constraintChecker{ctx: ctx, report: &r, add: func(path, code, message string) {
			r.Diagnostics = append(r.Diagnostics, Diagnostic{path, code, message})
		}}
		c.checkFormat(map[string]any{"format": "date"}, "PRIVATE_!!", "/value", true)
		constraintWant(t, r, true)
	})
}

func TestSchemaFormatInspectionPreservesInputsAndPrivacy(t *testing.T) {
	property := map[string]any{"type": "object", "properties": map[string]any{
		"date":    map[string]any{"type": "string", "format": "date", "default": "PRIVATE_DEFAULT", "example": "PRIVATE_EXAMPLE"},
		"byte":    map[string]any{"type": "string", "format": "byte"},
		"unknown": map[string]any{"type": "string", "format": "PRIVATE_SCHEMA_FORMAT"},
		"nested":  map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "uuid"}},
		"map":     map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string", "format": "date"}},
	}}
	d, raw := constraintDocument(t, constraintEncode(t, property))
	rawBefore := append([]byte(nil), raw...)
	object := constraintObject(map[string]any{
		"date": "PRIVATE_DATE_VALUE", "byte": "PRIVATE_BYTE_VALUE🚢", "unknown": "PRIVATE_UNKNOWN_VALUE",
		"nested": []any{"123e4567-e89b-12d3-a456-426614174000", "PRIVATE_UUID_VALUE"},
		"map":    map[string]any{"a/b~c": "PRIVATE_MAP_VALUE"},
	})
	objectBefore, documentBefore := constraintEncode(t, object), constraintEncode(t, d.root)
	want, err := d.Check(context.Background(), object)
	if err != nil {
		t.Fatal(err)
	}
	constraintWant(t, want, true, "format", "format", "format", "format")
	for _, diagnostic := range want.Diagnostics {
		if diagnostic.Pointer != "/value/date" && diagnostic.Pointer != "/value/byte" && diagnostic.Pointer != "/value/nested/1" && diagnostic.Pointer != "/value/map/a~1b~0c" {
			t.Fatal("format inspection lost a nested original-source pointer")
		}
	}
	if strings.Contains(want.Text()+constraintEncode(t, want), "PRIVATE_") {
		t.Fatal("format report disclosed source, schema, default or example")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := d.Check(context.Background(), object)
			if e != nil || !reflect.DeepEqual(r, want) {
				t.Error("concurrent read-only format inspection changed its result", e)
			}
		}()
	}
	wg.Wait()
	if objectBefore != constraintEncode(t, object) || documentBefore != constraintEncode(t, d.root) || !bytes.Equal(rawBefore, raw) {
		t.Fatal("format inspection changed draft, document or original source")
	}
}
