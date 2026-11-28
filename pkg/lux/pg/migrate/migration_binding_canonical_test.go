package migrate

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

const canonicalBindingFixture = `{"targets": [{"oid": 428812, "name": "public.audit_target"}, {"oid": 0, "name": null}], "version": 1, "tablespace": 1663, "tablespace_value": "", "permanent_schemas": "{11,2200}"}`

var canonicalRecognizeSink migrationExecutionBinding

func decodeBindingBeforeSpecialization(encoded string) (migrationExecutionBinding, error) {
	var result migrationExecutionBinding
	if encoded == "" {
		return result, nil
	}
	if err := json.Unmarshal([]byte(encoded), &result); err != nil {
		return result, fmt.Errorf("decode recorded migration execution binding: %w", err)
	}
	if result.Version != 1 {
		return result, errors.New("unknown version of the migration execution binding")
	}
	return result, nil
}

func TestCanonicalBindingCompatibility(t *testing.T) {
	replace := func(original, updated string) string {
		return strings.Replace(canonicalBindingFixture, original, updated, 1)
	}
	for _, sample := range []struct {
		name, raw string
		canonical bool
	}{
		{"common", canonicalBindingFixture, true},
		{"max_oid", replace("428812", "4294967295"), true},
		{"two_named_targets", replace(`{"oid": 0, "name": null}`, `{"oid": 15, "name": "public.other"}`), true},
		{"legacy_compact_targets", replace(`}, {"oid": 0`, `},{"oid": 0`), false},
		{"legacy_null_tablespace_value", replace(`"tablespace_value": ""`, `"tablespace_value": null`), false},
		{"legacy_extra_field_after_tablespace", replace(`"permanent_schemas":`, `"future_value": null, "permanent_schemas":`), false},
		{"legacy_target_name_before_oid", replace(`"oid": 428812, "name": "public.audit_target"`,
			`"name": "public.audit_target", "oid": 428812`), false},
		{"unterminated_permanent_schemas", canonicalBindingFixture[:len(canonicalBindingFixture)-2], false},
		{"old_absent", "", false},
		{"legacy_short_array", `{"version":1,"targets":[{"oid":15,"name":"first"}]}`, false},
		{"extra_field", replace(`"version": 1`, `"version": 1, "old_part": [{"nested":true}]`), false},
		{"missing_field", replace(`"tablespace_value": "", `, ""), true},
		{"projected_unknown_field", replace(`"tablespace_value": "", `, `"future_value": null, `), false},
		{"legacy_missing_tablespace_tail", replace(`, "tablespace_value": "", "permanent_schemas": "{11,2200}"`, ""), false},
		{"legacy_duplicate", replace(`"version": 1`, `"version": 1, "version": 1`), false},
		{"quoted_identifier", replace("public.audit_target", `\"audit quoted\".\"原表😀\"`), false},
		{"unescaped_unicode", replace("public.audit_target", "public.表"), false},
		{"invalid_utf8_replaced", replace("public.audit_target", "public.\xff"), false},
		{"terminal_escape", replace("public.audit_target", `public.\\"`), false},
		{"escaped_key", replace(`"version":`, `"\u0076ersion":`), false},
		{"case_insensitive_key", replace(`"version":`, `"VERSION":`), false},
		{"reordered_keys", replace(`"tablespace_value": "", "permanent_schemas": "{11,2200}"`,
			`"permanent_schemas": "{11,2200}", "tablespace_value": ""`), false},
		{"leading_space", " " + canonicalBindingFixture, false},
		{"trailing_space", canonicalBindingFixture + " ", false},
		{"trailing_data", canonicalBindingFixture + " null", false},
		{"overflow_oid", replace("428812", "4294967296"), false},
		{"leading_zero", replace("428812", "042"), false},
		{"negative_zero", replace("428812", "-0"), false},
		{"negative_oid", replace("428812", "-1"), false},
		{"decimal_oid", replace("428812", "1.25"), false},
		{"too_many_digits", replace("428812", "99999999999999999999999999999"), false},
		{"syntax_error", replace(`"name": null`, `"name": `), false},
		{"unknown_version", replace(`"version": 1`, `"version": 2`), false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			want, wantErr := decodeBindingBeforeSpecialization(sample.raw)
			got, gotErr := readMigrationBinding(sample.raw)
			if got != want || !reflect.DeepEqual(gotErr, wantErr) {
				t.Fatalf("got %+v (%v), expected %+v (%v)", got, gotErr, want, wantErr)
			}
			parsed, accepted := readCanonicalMigrationBinding(sample.raw)
			if accepted != sample.canonical || accepted && (wantErr != nil || parsed != want) {
				t.Fatalf("canonical parse: got %+v, accepted=%t; wanted accepted=%t", parsed, accepted, sample.canonical)
			}
			if allocs := testing.AllocsPerRun(256, func() {
				canonicalRecognizeSink, _ = readCanonicalMigrationBinding(sample.raw)
			}); allocs != 0 {
				t.Fatalf("canonical recognizer allocated %g times per operation", allocs)
			}
		})
	}
}
