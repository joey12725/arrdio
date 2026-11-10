package providers

import (
	"context"
	"fmt"
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

// Live Z-Library results for Persuasion: a summary product ranked first.
func TestRankDemotesSummaryProducts(t *testing.T) {
	in := jobs.Input{Title: "Persuasion", Author: "Jane Austen"}
	ebook := func(title, author string) jobs.Candidate {
		return jobs.Candidate{ID: title, Provider: "zlibrary", Title: title, Author: author, Language: "English", Format: "ebook", Extension: "epub"}
	}
	got := Rank(in, "ebook", []jobs.Candidate{
		ebook("Persuasion: Summarized for Busy People: A Memoir: Based on the Book by Jane Austen", "Quickread Press; Jane Austen"),
		ebook("Summary of Persuasion by Jane Austen: Key Takeaways & Analysis", "Jane Austen"),
		ebook("Persuasion", "Jane Austen"),
	})
	if len(got) == 0 || got[0].Title != "Persuasion" {
		t.Fatalf("a summary outranked the book: %v", got)
	}
}

func TestAuthorOverlapMatchesOneOfSeveralCredits(t *testing.T) {
	for _, c := range []struct {
		a, b string
		ok   bool
	}{
		{"Haruto Kimura and Sora Ito", "Haruto Kimura", true},
		{"Sora Ito & Haruto Kimura", "Haruto Kimura, Sora Ito", true},
		{"Emily St. John Mandel", "Mandel, Emily St. John", true},
		{"Jane Smith", "Smith, John", false},
		{"Haruto Kimura and Sora Ito", "Kenji Okada", false},
	} {
		if got := authorOverlap(c.a, c.b) >= 0.7; got != c.ok {
			t.Errorf("authorOverlap(%q, %q) = %v", c.a, c.b, authorOverlap(c.a, c.b))
		}
	}
}

// Live Kavita metadata: "Yuuto Hayashi" for a request read as "Yuto Hayashi", and
// "Clear, James" split into the writers Clear and James.
func TestWritersMatchRomanizationAndSplitNames(t *testing.T) {
	if !writersMatch("Yuto Hayashi", []string{"Yuuto Hayashi"}) {
		t.Error("romanization variant missed")
	}
	if !writersMatch("Samuel Smiles", []string{"Smiles", "Samuel"}) {
		t.Error("split writer names missed")
	}
	if writersMatch("Samuel Smiles", []string{"Samuel Butler"}) || writersMatch("Yuto Hayashi", []string{"Koji Hayashi"}) {
		t.Error("a different author matched")
	}
	if !oneEdit([]rune("clark"), []rune("clarke")) || oneEdit([]rune("smith"), []rune("smyth1")) {
		t.Error("oneEdit")
	}
}

// A book search names the volume in its title; Z-Library names it only in the
// candidate title. Another volume of the same series is not the requested work.
func TestRankRejectsOtherVolumeNamedInTitle(t *testing.T) {
	in := jobs.Input{Title: "Iron Lantern, Vol. 1", Author: "Daichi Morita"}
	got := Rank(in, "ebook", []jobs.Candidate{
		{ID: "v2", Title: "Iron Lantern, Vol. 2", Author: "Daichi Morita", Format: "ebook", Extension: "pdf"},
		{ID: "v1", Title: "Iron Lantern, Vol. 1", Author: "Daichi Morita", Format: "ebook", Extension: "pdf"},
		{ID: "box", Title: "Iron Lantern Vol. 1-11 Box Set", Author: "Daichi Morita", Format: "ebook", Extension: "epub"},
		{ID: "plain", Title: "Iron Lantern", Author: "Daichi Morita", Format: "ebook", Extension: "epub"},
	})
	for _, c := range got {
		if c.ID == "v2" {
			t.Fatal("other volume kept", got)
		}
	}
	if len(got) != 3 {
		t.Fatal(got)
	}
	novel := Rank(jobs.Input{Title: "The Bride of Lammermoor", Author: "Walter Scott"}, "ebook", []jobs.Candidate{{ID: "a", Title: "The Bride of Lammermoor", Author: "Walter Scott", Format: "ebook", Extension: "epub"}})
	if len(novel) != 1 {
		t.Fatal("unvolumed book filtered", novel)
	}
}

// Ebooks come from private trackers only, among ebook categories, and never as
// a container Kavita cannot read.
func TestProwlarrEbooksFromPrivateTrackersOnly(t *testing.T) {
	var searched []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/indexer" {
			w.Write([]byte(`[{"id":18,"name":"MyAnonamouse","enable":true,"privacy":"private","capabilities":{"categories":[{"id":7020,"name":"Books/EBook","subCategories":[]},{"id":7030,"name":"Books/Comics","subCategories":[]},{"id":3030,"name":"Audio/Audiobook","subCategories":[]}]}},
{"id":15,"name":"The Pirate Bay","enable":true,"privacy":"public","capabilities":{"categories":[{"id":7020,"name":"Books/EBook","subCategories":[]}]}}]`))
			return
		}
		searched = append(searched, r.URL.Query().Get("indexerIds")+":"+strings.Join(r.URL.Query()["categories"], ","))
		cats := `[{"id":7020,"name":"Books/EBook","subCategories":[]}]`
		w.Write([]byte(`[{"guid":"mobi","title":"The War of the Worlds by Herbert George Wells [ENG / MOBI]","infoHash":"a","seeders":9,"categories":` + cats + `},
{"guid":"epub","title":"The War of the Worlds by Herbert George Wells [ENG / EPUB]","infoHash":"b","seeders":4,"categories":` + cats + `}]`))
	}))
	defer server.Close()
	p := &Prowlarr{URL: server.URL, Key: "fixture"}
	got, e := p.Search(context.Background(), jobs.Input{Title: "The War of the Worlds", Author: "Herbert George Wells"}, "ebook")
	if e != nil || len(got) != 1 || got[0].ID != "epub" || got[0].Format != "ebook" {
		t.Fatal(got, e)
	}
	if strings.Join(searched, " ") != "18:7020" {
		t.Fatal("searched", searched)
	}
}

// Nyaa names volumes "Series v05" and packs "Series v01-20", without authors.
func TestProwlarrMangaTriesNyaaStyleQueries(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/indexer" {
			w.Write([]byte(`[{"id":16,"name":"Nyaa.si","enable":true,"privacy":"public","capabilities":{"categories":[{"id":156719,"name":"Literature  English-translated","subCategories":[]}]}}]`))
			return
		}
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		if q == "Iron Lantern" {
			w.Write([]byte(`[{"guid":"pack","title":"Iron Lantern v01-20 + 187-230 (2020-2025) (Digital) (scan0)","infoHash":"c","seeders":172,"categories":[{"id":156719,"name":"Literature  English-translated","subCategories":[]}]}]`))
			return
		}
		w.Write([]byte(`[]`))
	}))
	defer server.Close()
	p := &Prowlarr{URL: server.URL, Key: "fixture"}
	got, e := p.Search(context.Background(), jobs.Input{Title: "Iron Lantern", Author: "Daichi Morita", Volume: "5", Kind: "manga"}, "manga")
	if e != nil || len(got) != 1 || got[0].ID != "pack" {
		t.Fatal(got, e)
	}
	if strings.Join(queries, "|") != "Iron Lantern Daichi Morita 5|Iron Lantern v05|Iron Lantern" {
		t.Fatal(queries)
	}
	// A comic requested as an ebook searches the same way.
	queries = nil
	if got, _ := p.Search(context.Background(), jobs.Input{Title: "Iron Lantern", Author: "Daichi Morita", Volume: "5", Kind: "manga"}, "ebook"); len(got) != 1 || got[0].Format != "ebook" {
		t.Fatal("comic ebook", got)
	}
}

func TestReadableEbooksKeepsBestContainer(t *testing.T) {
	got := readableEbooks([]string{"/d/Book.mobi", "/d/Book.pdf", "/d/Book.epub", "/d/Book.azw3", "/d/cover.jpg", "/d/metadata.opf"})
	if len(got) != 1 || got[0] != "/d/Book.epub" {
		t.Fatal(got)
	}
	if got := readableEbooks([]string{"/d/Book.mobi", "/d/Book.azw3"}); len(got) != 0 {
		t.Fatal("unreadable kept", got)
	}
}

// Nyaa's Literature parent covers raws and other translations; manga searches
// only its English-translated child.
func TestMangaCategoriesSkipRawsAndForeignTranslations(t *testing.T) {
	nyaa := []Category{{ID: 7000, Name: "Books"}, {ID: 100920, Name: "Literature", Children: []Category{{ID: 156719, Name: "Literature  English-translated"}, {ID: 111160, Name: "Literature - Non-English-translated"}, {ID: 117084, Name: "Literature - Raw"}}}}
	if got := fmt.Sprint(categoryIDs(nyaa, "manga")); got != "[156719]" {
		t.Fatal(got)
	}
	mam := []Category{{ID: 7030, Name: "Books/Comics"}, {ID: 7020, Name: "Books/EBook"}, {ID: 100013, Name: "AudioBooks", Children: []Category{{ID: 100039, Name: "Audiobooks - Action/Adventure"}}}}
	if got := fmt.Sprint(categoryIDs(mam, "manga"), categoryIDs(mam, "ebook"), categoryIDs(mam, "audio")); got != "[7030] [7020] [100039]" {
		t.Fatal(got)
	}
}
