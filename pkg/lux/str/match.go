package str

import (
	"hash/maphash"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

const (
	regexCacheSets        = 32
	regexCacheWays        = 64
	regexCachePatternSize = 8192
	regexCacheWork        = 8192
	regexCacheSetWork     = 32768
)

type regexCacheEntry struct {
	pattern string
	value   *regexp.Regexp
	work    int
}

type regexCacheSet struct {
	entries [regexCacheWays]atomic.Pointer[regexCacheEntry]
	mu      sync.Mutex
	next    int
	work    int
}

type regexMatcherCache struct {
	sets [regexCacheSets]regexCacheSet
	seed maphash.Seed
}

var regexCache = newRegexMatcherCache()

func newRegexMatcherCache() *regexMatcherCache {
	return &regexMatcherCache{seed: maphash.MakeSeed()}
}

// Matches returns true if s matches the regex pattern.
// Invalid patterns return false. Cache admission never restricts matching inputs.
func Matches(pattern, s string) bool {
	return regexCache.matches(pattern, s)
}

func (c *regexMatcherCache) matches(pattern, s string) bool {
	if len(pattern) > regexCachePatternSize {
		matched, _ := regexp.MatchString(pattern, s)
		return matched
	}
	set := &c.sets[maphash.String(c.seed, pattern)&(regexCacheSets-1)]
	for index := range regexCacheWays {
		if entry := set.entries[index].Load(); entry != nil && entry.pattern == pattern {
			return entry.value.MatchString(s)
		}
	}
	return set.matchMiss(pattern, s)
}

func (set *regexCacheSet) matchMiss(pattern, s string) bool {
	work := regexCachePatternWork(pattern)
	if work == 0 {
		matched, _ := regexp.MatchString(pattern, s)
		return matched
	}
	owned := strings.Clone(pattern)
	compiled, err := regexp.Compile(owned)
	if err != nil {
		return false
	}
	return set.publish(&regexCacheEntry{pattern: owned, value: compiled, work: work}).MatchString(s)
}

func (set *regexCacheSet) publish(entry *regexCacheEntry) *regexp.Regexp {
	set.mu.Lock()
	defer set.mu.Unlock()
	for index := range regexCacheWays {
		cached := set.entries[index].Load()
		if cached != nil && cached.pattern == entry.pattern {
			return cached.value
		}
	}
	for {
		index := set.next
		set.next = (index + 1) % regexCacheWays
		if removed := set.entries[index].Swap(nil); removed != nil {
			set.work -= removed.work
		}
		if set.work+entry.work <= regexCacheSetWork {
			set.entries[index].Store(entry)
			set.work += entry.work
			return entry.value
		}
	}
}

// Estimate admission work, not heap bytes. Oversized patterns still execute uncached.
func regexCachePatternWork(pattern string) int {
	if len(pattern) > regexCachePatternSize {
		return 0
	}
	units, repeats, unicodeClasses := len(pattern), 1, 0
	for index := 0; index < len(pattern); index++ {
		if pattern[index] == '\\' && index+1 < len(pattern) {
			index++
			if pattern[index] == 'p' || pattern[index] == 'P' {
				unicodeClasses += 512
				if index+1 < len(pattern) && pattern[index+1] == '{' {
					end := strings.IndexByte(pattern[index+2:], '}')
					if end < 0 {
						return 0
					}
					index += end + 2
				}
			}
			continue
		}
		if pattern[index] != '{' {
			continue
		}
		end := strings.IndexByte(pattern[index+1:], '}')
		if end < 0 {
			return 0
		}
		bound, ok := regexRepeatCacheBound(pattern[index+1 : index+1+end])
		if !ok || bound > regexCacheWork/repeats {
			return 0
		}
		repeats *= bound
		units -= end + 2
		index += end + 1
	}
	work := max(1, units)*repeats + unicodeClasses
	if work > regexCacheWork {
		return 0
	}
	return work
}

func regexRepeatCacheBound(spec string) (int, bool) {
	minimum, maximum, ranged := strings.Cut(spec, ",")
	if ranged && maximum != "" {
		spec = maximum
	} else {
		spec = minimum
	}
	value, err := strconv.ParseUint(spec, 10, 12)
	if err != nil {
		return 0, false
	}
	if ranged && maximum == "" {
		value++
	}
	return max(1, int(value)), true
}
