package providers

import (
	"context"
	"github.com/ashkaty/arrdio-api/jobs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseTagParsesMyAnonamouseTitles(t *testing.T) {
	for _, tc := range []struct{ title, author, language, extension string }{
		{"White Fang by Jack London [ENG / M4B]", "Jack London", "eng", "m4b"},
		{"White Fang by Jack London [ENG / EPUB MOBI] [VIP]", "Jack London", "eng", ""},
		{"Tried by Fire by Mark Twain [ENG / MP3]", "Mark Twain", "eng", "mp3"},
		{"Good Omens by Terry Pratchett, Neil Gaiman [DUT / M4B]", "Terry Pratchett, Neil Gaiman", "dut", "m4b"},
		{"Jack London - White Fang", "", "", ""},
		{"Dracula [ENG / M4B]", "", "", ""},
	} {
		author, language, extension := releaseTag(tc.title)
		if author != tc.author || language != tc.language || extension != tc.extension {
			t.Fatalf("%q: got %q %q %q", tc.title, author, language, extension)
		}
	}
}

// Shapes match a live Prowlarr 2.6 MyAnonamouse response, including a category
// with no name and an untagged release title.
func TestProwlarrLanguageFromReleaseTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/indexer" {
			w.Write([]byte(`[{"id":18,"name":"MyAnonamouse","enable":true,"capabilities":{"categories":[{"id":3030,"name":"Audio/Audiobook","subCategories":[]}]}}]`))
			return
		}
		cats := `[{"id":3030,"name":"Audio/Audiobook","subCategories":[]},{"id":100047,"subCategories":[]}]`
		w.Write([]byte(`[` +
			`{"guid":"ita","title":"White Fang by Jack London [ITA / M4B]","infoHash":"a","seeders":900,"categories":` + cats + `},` +
			`{"guid":"eng","title":"White Fang by Jack London [ENG / M4B]","infoHash":"b","seeders":40,"categories":` + cats + `},` +
			`{"guid":"untagged","title":"Jack London - White Fang","infoHash":"c","seeders":10,"categories":` + cats + `}]`))
	}))
	defer server.Close()
	p := &Prowlarr{URL: server.URL, Key: "fixture"}
	ctx := context.Background()

	open, e := p.Search(ctx, jobs.Input{Title: "White Fang", Author: "Jack London"}, "audio")
	if e != nil || len(open) != 3 || open[0].ID != "eng" || open[1].ID != "untagged" || open[2].ID != "ita" {
		t.Fatal("unconstrained English preference", open, e)
	}
	if open[0].Language != "eng" || open[0].Extension != "m4b" || open[0].Author != "Jack London" {
		t.Fatal("release tag facts", open[0])
	}

	english, e := p.Search(ctx, jobs.Input{Title: "White Fang", Author: "Jack London", Constraints: jobs.Constraints{Language: "English"}}, "audio")
	if e != nil || len(english) != 1 || english[0].ID != "eng" {
		t.Fatal("English constraint", english, e)
	}
	italian, e := p.Search(ctx, jobs.Input{Title: "White Fang", Author: "Jack London", Constraints: jobs.Constraints{Language: "it"}}, "audio")
	if e != nil || len(italian) != 1 || italian[0].ID != "ita" {
		t.Fatal("ISO 639-2 language code", italian, e)
	}
	m4b, e := p.Search(ctx, jobs.Input{Title: "White Fang", Author: "Jack London", Constraints: jobs.Constraints{Container: ".m4b"}}, "audio")
	if e != nil || len(m4b) != 2 || m4b[0].ID != "eng" {
		t.Fatal("container constraint", m4b, e)
	}
}
func TestTorrentAddUsesOperatorCategoryInAutomaticMode(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			w.Write([]byte("[]"))
		case "/api/v2/torrents/add":
			r.ParseForm()
			form = r.PostForm
			w.Write([]byte("Ok."))
		}
	}))
	defer server.Close()
	hash := strings.Repeat("ab", 20)
	torrent := &Torrent{URL: server.URL, Category: "arrdio", DownloadRoot: t.TempDir(), Instance: "fixture"}
	c := jobs.Candidate{ID: "eng", Format: "audio", URL: "magnet:?xt=urn:btih:" + hash}
	r, e := torrent.Add(context.Background(), c, filepath.Join(t.TempDir(), "7", "audio"))
	if e != nil || r.ID != hash {
		t.Fatal(r, e)
	}
	if form.Get("category") != "arrdio" || form.Get("autoTMM") != "true" || form.Get("savepath") != "" || form.Get("tags") != r.OwnerTag {
		t.Fatal("torrent add form", form)
	}
}
