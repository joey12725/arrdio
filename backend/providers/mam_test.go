package providers

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/ashkaty/arrdio-api/jobs"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestInfoHashOfBencodedTorrent(t *testing.T) {
	info := "d6:lengthi12e4:name8:book.m4b12:piece lengthi16384e6:pieces20:" + strings.Repeat("x", 20) + "e"
	file := []byte("d8:announce16:http://t.invalid4:info" + info + "e")
	sum := sha1.Sum([]byte(info))
	if got, e := infoHash(file); e != nil || got != hex.EncodeToString(sum[:]) {
		t.Fatal(got, e)
	}
	for _, bad := range []string{"", "le", "d4:infoi1ee", "d4:info", "d3:key5:shorte"} {
		if _, e := infoHash([]byte(bad)); e == nil {
			t.Fatal("accepted", bad)
		}
	}
}

// MyAnonamouse results carry only Prowlarr's download proxy URL. ARRDIO must fetch
// the .torrent itself from Prowlarr's origin and upload it, not hand the URL to a
// client that may not reach Prowlarr, and must not reject it for lacking a hash.
func TestProwlarrPrivateReleaseUploadsTorrentFile(t *testing.T) {
	info := "d6:lengthi12e4:name8:book.m4b12:piece lengthi16384e6:pieces20:" + strings.Repeat("y", 20) + "e"
	file := []byte("d8:announce16:http://t.invalid4:info" + info + "e")
	sum := sha1.Sum([]byte(info))
	want := hex.EncodeToString(sum[:])
	grabs := 0
	prowlarr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/18/download" && r.Header.Get("X-Api-Key") == "fixture":
			grabs++
			w.Write(file)
		case r.URL.Path == "/16/download":
			http.Redirect(w, r, "magnet:?xt=urn:btih:"+strings.Repeat("cd", 20), 301)
		default:
			w.WriteHeader(404)
		}
	}))
	defer prowlarr.Close()
	var uploaded []byte
	var fields url.Values
	qbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			w.Write([]byte("[]"))
		case "/api/v2/torrents/add":
			if r.ParseMultipartForm(1<<20) == nil {
				f, _, _ := r.FormFile("torrents")
				uploaded, _ = io.ReadAll(f)
				fields = url.Values(r.MultipartForm.Value)
			} else {
				r.ParseForm()
				fields = r.PostForm
			}
			w.Write([]byte("Ok."))
		}
	}))
	defer qbit.Close()
	p := &Prowlarr{URL: prowlarr.URL, Key: "fixture", Torrent: &Torrent{URL: qbit.URL, Category: "arrdio", DownloadRoot: t.TempDir(), Instance: "fixture"}}
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "9", "audio")
	// Prowlarr echoes the host it was queried on; the configured origin is used instead.
	private := jobs.Candidate{ID: "mam", Format: "audio", URL: "http://elsewhere.invalid:9696/18/download?apikey=fixture&link=abc", Extra: []byte(`{"hash":"","format":"audio"}`)}
	prepared, e := p.Prepare(ctx, private, dir)
	if e != nil || prepared.ID != want {
		t.Fatal("prepare", prepared, e)
	}
	r, e := p.Acquire(ctx, private, dir)
	if e != nil || r.ID != want || string(uploaded) != string(file) || fields.Get("urls") != "" || fields.Get("category") != "arrdio" || fields.Get("autoTMM") != "true" {
		t.Fatal("upload", r, e, fields)
	}
	if grabs != 1 {
		t.Fatal("private tracker grabbed more than once per release", grabs)
	}
	redirect := jobs.Candidate{ID: "nyaa", Format: "manga", URL: prowlarr.URL + "/16/download?apikey=fixture&link=x"}
	if r, e := p.Acquire(ctx, redirect, dir); e != nil || r.ID != strings.Repeat("cd", 20) || !strings.HasPrefix(fields.Get("urls"), "magnet:") {
		t.Fatal("magnet redirect", r, e, fields)
	}
	foreign := jobs.Candidate{ID: "x", Format: "audio", URL: "https://tracker.invalid/get?id=1"}
	if _, e := p.Acquire(ctx, foreign, dir); e == nil {
		t.Fatal("non-Prowlarr link accepted")
	}
}

// slskd 0.26 writes music\Artist\Album\01.flac to <downloads>/Album/01.flac.
func TestSoulseekCompletedFileUsesSlskdLayout(t *testing.T) {
	if got := peerLocalName(`@@share\Audiobooks\Jack London\White Fang\01 - Part.mp3`); got != "White Fang/01 - Part.mp3" {
		t.Fatal(got)
	}
	if got := peerLocalName("single.m4b"); got != "single.m4b" {
		t.Fatal(got)
	}
	if got := peerLocalName(`a\..\..\escape.mp3`); got != "../escape.mp3" {
		t.Fatal("layout", got)
	}
}

func TestStalledTorrentFailsOverAfterTimeout(t *testing.T) {
	hash := strings.Repeat("ef", 20)
	var state string
	var added int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			fmt.Fprintf(w, `[{"progress":0,"state":%q,"added_on":%d,"last_activity":0,"tags":"x"}]`, state, added)
		case "/api/v2/torrents/files":
			w.Write([]byte(`[{"index":0,"name":"book.m4b","progress":0,"priority":1,"size":10}]`))
		}
	}))
	defer server.Close()
	torrent := &Torrent{URL: server.URL, DownloadRoot: t.TempDir()}
	for _, tc := range []struct {
		state string
		age   time.Duration
		fails bool
	}{
		{"stalledDL", 5 * time.Hour, true},
		{"metaDL", 5 * time.Hour, true},
		{"stalledDL", time.Hour, false},
		{"downloading", 5 * time.Hour, false},
	} {
		state, added = tc.state, time.Now().Add(-tc.age).Unix()
		_, e := torrent.Poll(context.Background(), jobs.Receipt{ID: hash})
		var retry jobs.RetryTransferFailure
		if errors.As(e, &retry) != tc.fails {
			t.Fatal(tc, e)
		}
	}
}

// qBittorrent 5.1 keeps a partially selected pack in its incomplete folder after
// the wanted volume finishes (seen live with a nine-volume manga pack).
func TestTorrentFilesResolveFromReportedClientPaths(t *testing.T) {
	root := t.TempDir()
	legacy := &Torrent{DownloadRoot: root}
	if p, e := legacy.localPath("/downloads/prowlarr/Book", "/downloads/prowlarr", "/downloads/incomplete", "Book/01.mp3"); e != nil || p != filepath.Join(root, "Book/01.mp3") {
		t.Fatal("legacy", p, e)
	}
	mapped := &Torrent{DownloadRoot: root, ClientRoot: "/downloads"}
	for _, tc := range []struct{ content, want string }{
		{"/downloads/prowlarr/Pack", "prowlarr/Pack/v01.cbz"},
		{"/downloads/incomplete/prowlarr/Pack", "incomplete/prowlarr/Pack/v01.cbz"},
	} {
		base := "/downloads/incomplete/prowlarr"
		if p, e := mapped.localPath(tc.content, "/downloads/prowlarr", base, "Pack/v01.cbz"); e != nil || p != filepath.Join(root, tc.want) {
			t.Fatal(tc, p, e)
		}
	}
	if _, e := mapped.localPath("/elsewhere/Pack", "/elsewhere", "", "Pack/v01.cbz"); e == nil {
		t.Fatal("path outside the mounted client root accepted")
	}
	if _, e := legacy.localPath("", "", "", "../escape.m4b"); e == nil {
		t.Fatal("traversal accepted")
	}
}
