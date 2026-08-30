package home

import (
	"sort"
	"strings"
)

// match is one fuzzy-search hit.
type match struct {
	name      string
	score     int
	positions []int // indexes into name of the matched runes
}

// fuzzyMatch reports whether every rune of query appears in name in order,
// with a score that favours prefixes and consecutive runs - the usual
// launcher heuristic, so "snk" finds snake and "tet" ranks tetris first.
func fuzzyMatch(query, name string) (match, bool) {
	q := []rune(strings.ToLower(query))
	n := []rune(strings.ToLower(name))
	if len(q) == 0 {
		return match{name: name}, true
	}
	positions := make([]int, 0, len(q))
	score := 0
	qi := 0
	prev := -2
	for ni := 0; ni < len(n) && qi < len(q); ni++ {
		if n[ni] != q[qi] {
			continue
		}
		positions = append(positions, ni)
		switch {
		case ni == 0:
			score += 20 // prefix
		case ni == prev+1:
			score += 10 // consecutive
		default:
			score += 2 - (ni - prev - 1) // gap penalty
		}
		prev = ni
		qi++
	}
	if qi < len(q) {
		return match{}, false
	}
	// Shorter names win ties: "pong" over "pong-deluxe".
	score -= len(n) - len(q)
	return match{name: name, score: score, positions: positions}, true
}

// fuzzySearch ranks names against query, best first.
func fuzzySearch(query string, names []string) []match {
	var out []match
	for _, name := range names {
		if m, ok := fuzzyMatch(query, name); ok {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].score > out[j].score })
	return out
}
