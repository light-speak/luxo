package semantic

import "testing"

func TestCacheRejectsNonPolicyArguments(t *testing.T) {
	for _, annotation := range []string{
		"@cache", "@cache()", "@cache(ttl: 0)", "@cache(ttl: 0s)", "@cache(ttl: -1)",
		"@cache(ttl: -1m)", `@cache(ttl: "5m")`, "@cache(ttl: 1.5)", "@cache(ttl: true)",
		"@cache(ttl: null)", "@cache(ttl: duration)", "@cache(tll: 5m)",
		"@cache(ttl: 1m, ttl: 2m)", "@cache(ttl: 1m, other: 2)",
		"@cache(ttl: 9223372037)", "@cache(ttl: 9223372036854775807s)",
		"@cache(ttl: 106752d)", "@cache(ttl: 9223372036855ms)",
	} {
		t.Run(annotation, func(t *testing.T) {
			expectError(t, analyze(t, "api cached(): Int "+annotation+" { return 1 }"), "@cache")
		})
	}
}
