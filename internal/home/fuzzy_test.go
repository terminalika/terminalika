package home

import "testing"

var games = []string{"dino", "invaders", "snake", "tetris"}

func TestFuzzySearchRanksPrefixFirst(t *testing.T) {
	got := fuzzySearch("s", games)
	if len(got) == 0 || got[0].name != "snake" {
		t.Fatalf("fuzzySearch(s) = %+v, want snake first", got)
	}
	got = fuzzySearch("te", games)
	if len(got) != 1 || got[0].name != "tetris" {
		t.Fatalf("fuzzySearch(te) = %+v", got)
	}
	got = fuzzySearch("snk", games)
	if len(got) != 1 || got[0].name != "snake" || len(got[0].positions) != 3 {
		t.Fatalf("fuzzySearch(snk) = %+v", got)
	}
}

func TestFuzzySearchNoMatch(t *testing.T) {
	if got := fuzzySearch("zzz", games); len(got) != 0 {
		t.Fatalf("fuzzySearch(zzz) = %+v", got)
	}
	if got := fuzzySearch("", games); len(got) != len(games) {
		t.Fatalf("empty query should list everything, got %d", len(got))
	}
}

func TestFuzzyMatchIsCaseInsensitive(t *testing.T) {
	if _, ok := fuzzyMatch("DINO", "dino"); !ok {
		t.Fatal("case-insensitive match expected")
	}
}
