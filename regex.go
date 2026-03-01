package main

import (
	"regexp"
	"sync"
)

// regexCache caches compiled regexes.
var (
	regexCacheMu sync.Mutex
	regexCache   = make(map[string]*regexp.Regexp)
)

// mustCompile compiles a regex, caching the result.
func mustCompile(pattern string) *regexp.Regexp {
	regexCacheMu.Lock()
	defer regexCacheMu.Unlock()
	if re, ok := regexCache[pattern]; ok {
		return re
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		// Fall back to literal
		re = regexp.MustCompile(regexp.QuoteMeta(pattern))
	}
	regexCache[pattern] = re
	return re
}

// matchRegex returns true if s matches pattern anywhere.
func matchRegex(pattern, s string) bool {
	return mustCompile(pattern).MatchString(s)
}
