package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ashkaty/arrdio-api/jobs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRequestedVolumeCannotMatchDifferentOrUnknownCoverage(t *testing.T) {
	in := jobs.Input{Title: "Example Manga", Kind: "manga", Volume: "1"}
	got := Rank(in, "manga", []jobs.Candidate{{ID: "wrong", Title: "Example Manga v99", Format: "manga", Volume: volumeCoverage("Example Manga v99")}, {ID: "unknown-pack", Title: "Example Manga complete", Format: "manga"}, {ID: "matching-pack", Title: "Example Manga v01-12", Format: "manga", Volume: volumeCoverage("Example Manga v01-12")}})
	if len(got) != 1 || got[0].ID != "matching-pack" {
		t.Fatal(got)
	}
	if coversVolume(volumeCoverage("Series v10/file.cbz"), "1") {
		t.Fatal("substring volume collision")
	}
	if fileMatchesVolume("Series v01-16/Series v02.cbz", "1") || fileMatchesVolume("Series v01-16.cbz", "1") || !fileMatchesVolume("Series v01-16/Series v01.cbz", "1") {
		t.Fatal("pack expanded beyond requested volume")
	}
}
func TestKavitaVolumePresenceAndSessionRenewal(t *testing.T) {
	auths := 0
	reject := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/Plugin/authenticate":
			auths++
			json.NewEncoder(w).Encode(map[string]string{"token": "renewed"})
		case "/api/Series/v2":
			w.Header().Set("Pagination", `{"totalPages":1}`)
			if reject {
				reject = false
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`[{"id":42,"name":"Example Manga","format":2}]`))
		case "/api/Series/metadata":
			w.Write([]byte(`{"writers":[{"name":"Writer"}]}`))
		case "/api/Series/volumes":
			w.Write([]byte(`[{"name":"Volume 1","minNumber":1,"maxNumber":1,"chapters":[{"number":"1","files":[{"filePath":"/books/Example Manga v01.cbz"}]}]}]`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	library := &Libraries{KavitaURL: server.URL, KavitaToken: "expired", KavitaKey: "fixture", Routes: []Route{{Category: "manga", Format: "manga", Service: "kavita", LibraryID: "2", Path: t.TempDir()}}}
	in := jobs.Input{Title: "Example Manga", Author: "Writer", Kind: "manga", Volume: "1"}
	if _, e := library.Presence(context.Background(), in, "manga"); e == nil {
		t.Fatal("expired authentication treated as absent")
	}
	p, e := library.Presence(context.Background(), in, "manga")
	if e != nil || p[0].State != "available" || auths != 1 {
		t.Fatal(p, e, auths)
	}
	in.Volume = "2"
	p, e = library.Presence(context.Background(), in, "manga")
	if e != nil || p[0].State != "absent" {
		t.Fatal("missing volume blocked by existing series", p, e)
	}
}
func TestSoulseekRequiresContiguousCompleteInventory(t *testing.T) {
	if completeSet([]peerFile{{Filename: "01 chapter.mp3"}, {Filename: "03 chapter.mp3"}}) {
		t.Fatal("missing chapter accepted")
	}
	if completeSet([]peerFile{{Filename: "book.mp3"}}) {
		t.Fatal("single search MP3 accepted")
	}
	if !completeSet([]peerFile{{Filename: "01 chapter.mp3"}, {Filename: "02 chapter.mp3"}}) {
		t.Fatal("complete numbered set rejected")
	}
	// Live slskd shares name tracks in many ways; all of these are complete.
	numbered := func(format string, n int) []peerFile {
		out := []peerFile{}
		for i := 1; i <= n; i++ {
			out = append(out, peerFile{Filename: fmt.Sprintf(format, i)})
		}
		return out
	}
	for _, format := range []string{`Dracula\Dracula %d.mp3`, `Dracula\Bram Stoker - Dracula %02d-18.mp3`, `Dracula (1965) 64k\Part %02d of Dracula (1965).mp3`, `%02d - Chapter %[1]d.m4b`} {
		if !completeSet(numbered(format, 18)) {
			t.Fatal("complete set rejected:", format)
		}
	}
	if completeSet(append(numbered(`Disc 1 - %02d.mp3`, 3), numbered(`Disc 2 - %02d.mp3`, 3)...)) {
		t.Fatal("per-disc numbering guessed as complete")
	}
	if completeSet(numbered(`Book (2007) %d.mp3`, 5)[1:]) {
		t.Fatal("set missing track 1 accepted")
	}
}
func TestABBExtractsHashAcrossMarkup(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.Write([]byte(`<a href="/abss/book">The Jungle</a>`))
			return
		}
		w.Write([]byte(`<title>The Jungle - Upton Sinclair</title><table><tr><td>Info Hash:</td><td>0123456789abcdef0123456789abcdef01234567</td></tr></table>`))
	}))
	defer server.Close()
	source := &ABB{URL: server.URL}
	results, e := source.Search(context.Background(), jobs.Input{Title: "The Jungle", Author: "Upton Sinclair"}, "audio")
	if e != nil || len(results) != 1 || results[0].ID != "0123456789abcdef0123456789abcdef01234567" {
		t.Fatal(results, e)
	}
}
func TestAudioImportPreservesMultiDiscAndIgnoresNFO(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg unavailable")
	}
	root := t.TempDir()
	files := []string{}
	for _, disc := range []string{"CD1", "CD2"} {
		dir := filepath.Join(root, disc)
		os.Mkdir(dir, 0700)
		path := filepath.Join(dir, "01.mp3")
		if e := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=duration=0.2", "-c:a", "libmp3lame", path).Run(); e != nil {
			t.Fatal(e)
		}
		files = append(files, path)
	}
	nfo := filepath.Join(root, "release.nfo")
	os.WriteFile(nfo, []byte("metadata"), 0600)
	files = append(files, nfo)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"total":1,"results":[{"id":"book","media":{"metadata":{"title":"A","authorName":"Writer"},"numAudioFiles":2}}]}`))
	}))
	defer server.Close()
	destination := t.TempDir()
	library := &Libraries{ABSURL: server.URL, ABSToken: "fixture", Routes: []Route{{Category: "audio", Format: "audio", Service: "audiobookshelf", LibraryID: "1", Path: destination}}}
	presence, e := library.Import(context.Background(), jobs.Input{Title: "A", Author: "Writer"}, "audio", files, "")
	if e != nil || presence[0].State != "available" {
		t.Fatal(presence, e)
	}
	matches, _ := filepath.Glob(filepath.Join(destination, "Writer", "A", "*.mp3"))
	if len(matches) != 2 {
		t.Fatal(matches)
	}
	for _, src := range files {
		if _, e := os.Stat(src); e != nil {
			t.Fatal("source modified")
		}
	}
}

// Live Kavita filed The Bride of Lammermoor under the series "The Waverley
// Novels"; only its chapter title names the book.
func TestKavitaFindsBookInsideDifferentlyNamedSeries(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/Series/v2":
			w.Header().Set("Pagination", `{"totalPages":1}`)
			w.Write([]byte(`[{"id":6072,"name":"The Waverley Novels"}]`))
		case "/api/Search/search":
			w.Write([]byte(`{"chapters":[{"titleName":"The Bride of Lammermoor","volumeId":16323,"files":[{"filePath":"/books/Fiction/The Waverley Novels/The Bride of Lammermoor (Walter Scott).epub"}],"writers":[{"name":"Walter Scott"}]}]}`))
		case "/api/Volume":
			w.Write([]byte(`{"id":16323,"seriesId":6072}`))
		case "/api/Series/6072":
			w.Write([]byte(`{"id":6072,"libraryId":1}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	library := &Libraries{KavitaURL: server.URL, KavitaToken: "fixture", Routes: []Route{{Category: "fiction", Format: "ebook", Service: "kavita", LibraryID: "1", Path: t.TempDir()}}}
	p, e := library.Presence(context.Background(), jobs.Input{Title: "The Bride of Lammermoor", Author: "Walter Scott"}, "ebook")
	if e != nil || p[0].State != "available" || p[0].ItemID != "6072" {
		t.Fatal(p, e)
	}
	p, e = library.Presence(context.Background(), jobs.Input{Title: "The Bride of Lammermoor", Author: "Someone Else"}, "ebook")
	if e != nil || p[0].State != "absent" {
		t.Fatal("another author's book matched", p, e)
	}
	library.Routes[0].LibraryID = "3"
	p, e = library.Presence(context.Background(), jobs.Input{Title: "The Bride of Lammermoor", Author: "Walter Scott"}, "ebook")
	if e != nil || p[0].State != "absent" {
		t.Fatal("a book in another library matched", p, e)
	}
}
