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
		// Try as a POSIX ERE — fall back to literal
		re = regexp.MustCompile(regexp.QuoteMeta(pattern))
	}
	regexCache[pattern] = re
	return re
}

// mustCompileFS compiles a field-separator regex.
func mustCompileFS(fs string) *regexp.Regexp {
	return mustCompile(fs)
}

// matchRegex returns true if s matches pattern anywhere.
func matchRegex(pattern, s string) bool {
	return mustCompile(pattern).MatchString(s)
}

// structuralExtract finds all non-overlapping matches of pattern in text
// and returns them as a slice of [start, end] pairs.
func structuralExtract(pattern, text string) [][]int {
	re := mustCompile(pattern)
	return re.FindAllStringIndex(text, -1)
}

// structuralGaps returns the gaps between matches (the y-command complement).
func structuralGaps(pattern, text string) [][]int {
	matches := structuralExtract(pattern, text)
	var gaps [][]int
	prev := 0
	for _, m := range matches {
		if m[0] > prev {
			gaps = append(gaps, []int{prev, m[0]})
		}
		prev = m[1]
	}
	if prev < len(text) {
		gaps = append(gaps, []int{prev, len(text)})
	}
	return gaps
}
