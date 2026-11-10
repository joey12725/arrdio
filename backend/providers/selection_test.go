package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ashkaty/arrdio-api/jobs"
)

func seeded(title, author, ext string, seeders int) jobs.Candidate {
	return jobs.Candidate{ID: title + ext, Provider: "prowlarr", Title: title, Author: author, Language: "eng", Format: "audio", Extension: ext, Seeders: &seeders}
}

// Live MAM results for Dracula: the busiest release is MP3, but healthy M4B
// releases exist and need no lossy conversion.
func TestRankPrefersHealthyM4BOverBusierMP3(t *testing.T) {
	in := jobs.Input{Title: "Dracula", Author: "Bram Stoker"}
	got := Rank(in, "audio", []jobs.Candidate{
		seeded("Dracula by Bram Stoker [ENG / MP3]", "Bram Stoker", "mp3", 841),
		seeded("Dracula by Bram Stoker [ENG / M4B]", "Bram Stoker", "m4b", 617),
		seeded("Dracula by Bram Stoker [ENG / M4B]", "Bram Stoker", "m4b", 117),
	})
	if got[0].Extension != "m4b" || *got[0].Seeders != 617 || got[2].Extension != "mp3" {
		t.Fatalf("order = %v", got)
	}
}

func TestRankKeepsHealthySwarmOverThinBetterContainer(t *testing.T) {
	in := jobs.Input{Title: "Dracula", Author: "Bram Stoker"}
	got := Rank(in, "audio", []jobs.Candidate{
		seeded("Dracula by Bram Stoker [ENG / M4B]", "Bram Stoker", "m4b", 3),
		seeded("Dracula by Bram Stoker [ENG / MP3]", "Bram Stoker", "mp3", 400),
	})
	if got[0].Extension != "mp3" {
		t.Fatalf("a 3-seed M4B outranked a healthy MP3: %v", got)
	}
}

func TestRankDemotesUnrequestedAdaptations(t *testing.T) {
	in := jobs.Input{Title: "Riders of the Purple Sage", Author: "Zane Grey"}
	got := Rank(in, "audio", []jobs.Candidate{
		seeded("Riders of the Purple Sage: Adapted for Young Readers by Zane Grey [ENG / M4B]", "Zane Grey", "m4b", 500),
		seeded("Riders of the Purple Sage by Zane Grey [ENG / MP3] (Abridged)", "Zane Grey", "mp3", 400),
		seeded("Riders of the Purple Sage by Zane Grey [ENG / MP3] (Unabridged)", "Zane Grey", "mp3", 20),
	})
	if got[0].Seeders == nil || *got[0].Seeders != 20 {
		t.Fatalf("an adaptation outranked the unabridged release: %v", got)
	}
	asked := Rank(jobs.Input{Title: "Riders of the Purple Sage: Adapted for Young Readers", Author: "Zane Grey"}, "audio", got)
	if len(asked) == 0 || !unwantedVariant(jobs.Input{Title: "Riders of the Purple Sage"}, asked[0]) {
		t.Fatalf("an explicitly requested adaptation was demoted: %v", asked)
	}
}

func TestRankPrefersEPUBOverPDF(t *testing.T) {
	in := jobs.Input{Title: "Middlemarch", Author: "George Eliot"}
	got := Rank(in, "ebook", []jobs.Candidate{
		{ID: "1", Title: "Middlemarch", Author: "George Eliot", Format: "ebook", Extension: "pdf"},
		{ID: "2", Title: "Middlemarch", Author: "George Eliot", Format: "ebook", Extension: "epub"},
	})
	if got[0].Extension != "epub" {
		t.Fatalf("order = %v", got)
	}
}

// A spine read from a photo with no author matched an unrelated bird guide
// that shares four of its five title words.
func TestRankWithoutAuthorNeedsTitlePhrase(t *testing.T) {
	in := jobs.Input{Title: "A Field Guide to Bird Calls"}
	unrelated := jobs.Candidate{ID: "1", Title: "Coastal Bird Guide: Comprehensive Field Guide to over 950 Bird Species in Southern Africa", Author: "VARIOUS", Format: "ebook", Extension: "pdf"}
	if got := Rank(in, "ebook", []jobs.Candidate{unrelated}); len(got) != 0 {
		t.Fatalf("unrelated work accepted: %v", got)
	}
	real := jobs.Candidate{ID: "2", Title: "Field Guide to Bird Calls of North America", Author: "Someone", Format: "ebook", Extension: "epub"}
	if got := Rank(in, "ebook", []jobs.Candidate{real}); len(got) != 1 {
		t.Fatalf("matching title rejected: %v", got)
	}
	if !titlePhrase("The Wager", "The Wager: A Tale of Shipwreck, Mutiny and Murder") || !titlePhrase("Dracula", "Dracula by Bram Stoker [ENG / M4B]") || titlePhrase("Dracula", "Draculas of Transylvania") {
		t.Fatal("titlePhrase")
	}
}

func TestVolumeRangesInWords(t *testing.T) {
	for title, want := range map[string]string{
		"Elowen: Past Winter's End Volumes 1 to 13 and Chapters 128 to 140 by Haruto Kimura [ENG / CBZ]": "1-13",
		"Green Hour, Vol. 1 - Vol. 9 by Riku Nakamura [ENG / CBZ]":                                        "1-9",
		"Iron Lantern v01-v11 (Digital)": "01-11",
		"Elowen: Past Winter’s End, Vol. 1 by Haruto Kimura, Sora Ito [ENG / CBZ]": "1",
	} {
		if got := volumeCoverage(title); got != want {
			t.Errorf("%q: got %q, want %q", title, got, want)
		}
	}
}

// MAM titles vol. 1 "Winter’s End" (curly apostrophe), so the full subtitled
// query misses it; the main-title query finds it.
func TestProwlarrFallsBackToMainTitle(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/indexer" {
			w.Write([]byte(`[{"id":18,"name":"MyAnonamouse","enable":true,"capabilities":{"categories":[{"id":7030,"name":"Books/Comics","subCategories":[]}]}}]`))
			return
		}
		queries = append(queries, r.URL.Query().Get("query"))
		cats := `[{"id":7030,"name":"Books/Comics","subCategories":[]}]`
		if strings.Contains(r.URL.Query().Get("query"), "Winter") {
			w.Write([]byte(`[{"guid":"v8","title":"Elowen: Past Winter's End, Vol. 8 by Haruto Kimura [ENG / CBZ]","infoHash":"a","seeders":116,"categories":` + cats + `}]`))
			return
		}
		w.Write([]byte(`[{"guid":"v1","title":"Elowen: Past Winter’s End, Vol. 1 by Haruto Kimura, Sora Ito [ENG / CBZ] [VIP]","infoHash":"b","seeders":160,"categories":` + cats + `}]`))
	}))
	defer server.Close()
	p := &Prowlarr{URL: server.URL, Key: "fixture"}
	got, e := p.Search(context.Background(), jobs.Input{Title: "Elowen: Past Winter's End", Author: "Haruto Kimura and Sora Ito", Volume: "1", Kind: "manga"}, "manga")
	if e != nil || len(got) != 1 || got[0].ID != "v1" {
		t.Fatal(got, e)
	}
	if len(queries) != 2 || queries[1] != "Elowen Haruto Kimura 1" {
		t.Fatal(queries)
	}
}

func TestPrimaryAuthor(t *testing.T) {
	for in, want := range map[string]string{"Haruto Kimura and Sora Ito": "Haruto Kimura", "Haruto Kimura, Sora Ito": "Haruto Kimura", "Rand & Andy": "Rand", "Bram Stoker": "Bram Stoker", "": ""} {
		if got := primaryAuthor(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
}
