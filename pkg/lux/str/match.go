package str

import (
	"regexp"
	"sync"
)

// regexCache caches compiled regex patterns for .matches() calls.
// Avoids recompiling the same pattern on every request.
var regexCache sync.Map // map[string]*regexp.Regexp

// Matches returns true if s matches the regex pattern.
// Compiled patterns are cached — safe for high-throughput hot paths.
func Matches(pattern, s string) bool {
	var re *regexp.Regexp
	if cached, ok := regexCache.Load(pattern); ok {
		re = cached.(*regexp.Regexp)
	} else {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return false
		}
		regexCache.Store(pattern, compiled)
		re = compiled
	}
	return re.MatchString(s)
}
