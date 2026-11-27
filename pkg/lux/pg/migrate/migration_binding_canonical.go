package migrate

import "strings"

// readCanonicalMigrationBinding recognizes the order and separators of the
// PostgreSQL JSONB text written by queryExecutionBinding. General JSON,
// including quoted identifiers with escapes, stays with the legacy decoder.
func readCanonicalMigrationBinding(raw string) (migrationExecutionBinding, bool) {
	rest, ok := strings.CutPrefix(raw, `{"targets": [`)
	if !ok {
		return migrationExecutionBinding{}, false
	}

	var binding migrationExecutionBinding
	binding.Targets[0], rest, ok = cutMigrationBindingTarget(rest)
	if !ok {
		return migrationExecutionBinding{}, false
	}
	rest, ok = strings.CutPrefix(rest, ", ")
	if !ok {
		return migrationExecutionBinding{}, false
	}
	binding.Targets[1], rest, ok = cutMigrationBindingTarget(rest)
	if !ok {
		return migrationExecutionBinding{}, false
	}
	rest, ok = strings.CutPrefix(rest, `], "version": 1, "tablespace": `)
	if !ok {
		return migrationExecutionBinding{}, false
	}
	binding.Tablespace, rest, ok = cutMigrationBindingOID(rest, `, "`)
	if !ok {
		return migrationExecutionBinding{}, false
	}
	// The live projection omits the setting already consumed from the receipt.
	// Recorded bindings retain tablespace_value for restoration.
	if value, present := strings.CutPrefix(rest, `tablespace_value": `); present {
		binding.TablespaceValue, rest, ok = cutMigrationBindingString(value)
		if !ok {
			return migrationExecutionBinding{}, false
		}
		rest, ok = strings.CutPrefix(rest, `, "permanent_schemas": `)
	} else {
		rest, ok = strings.CutPrefix(rest, `permanent_schemas": `)
	}
	if !ok {
		return migrationExecutionBinding{}, false
	}
	binding.PermanentSchemas, rest, ok = cutMigrationBindingString(rest)
	if !ok || rest != "}" {
		return migrationExecutionBinding{}, false
	}
	binding.Version = 1
	return binding, true
}

func cutMigrationBindingTarget(raw string) (migrationBoundObject, string, bool) {
	rest, ok := strings.CutPrefix(raw, `{"oid": `)
	if !ok {
		return migrationBoundObject{}, "", false
	}
	oid, rest, ok := cutMigrationBindingOID(rest, `, "name": `)
	if !ok {
		return migrationBoundObject{}, "", false
	}
	if tail, null := strings.CutPrefix(rest, "null}"); null {
		return migrationBoundObject{OID: oid}, tail, true
	}
	name, rest, ok := cutMigrationBindingString(rest)
	if !ok {
		return migrationBoundObject{}, "", false
	}
	rest, ok = strings.CutPrefix(rest, "}")
	return migrationBoundObject{OID: oid, Name: name}, rest, ok
}

func cutMigrationBindingOID(raw, suffix string) (uint32, string, bool) {
	digits, rest, ok := strings.Cut(raw, suffix)
	if !ok || len(digits) == 0 || len(digits) > 10 || len(digits) > 1 && digits[0] == '0' {
		return 0, "", false
	}
	// A fixed digit count bounds the accumulator. No strconv error object
	// is needed when rejecting input that belongs to the legacy decoder.
	var oid uint64
	for i := range len(digits) {
		digit := digits[i] - '0'
		if digit > 9 {
			return 0, "", false
		}
		oid = oid*10 + uint64(digit)
	}
	if oid > 1<<32-1 {
		return 0, "", false
	}
	return uint32(oid), rest, true
}

func cutMigrationBindingString(raw string) (string, string, bool) {
	if len(raw) == 0 || raw[0] != '"' {
		return "", "", false
	}
	value, rest, ok := strings.Cut(raw[1:], `"`)
	if !ok {
		return "", "", false
	}
	for i := range len(value) {
		switch b := value[i]; {
		case b < ' ', b > '~', b == '\\':
			return "", "", false
		}
	}
	// The pgx text scan owns raw, and binding strings are consumed by
	// the same reconciliation operation.
	return value, rest, true
}
