package providers

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/ashkaty/arrdio-api/jobs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func writeTestEPUB(t *testing.T, path, content string) {
	t.Helper()
	var data bytes.Buffer
	z := zip.NewWriter(&data)
	m, _ := z.Create("mimetype")
	m.Write([]byte("application/epub+zip"))
	body, _ := z.Create("book.xhtml")
	body.Write([]byte("<html><body>" + content + "</body></html>"))
	z.Close()
	if e := os.WriteFile(path, data.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
}
func testOwnership() (*jobs.ImportHooks, map[string]jobs.OwnedPlacement) {
	owned := map[string]jobs.OwnedPlacement{}
	hooks := &jobs.ImportHooks{Protected: map[string]bool{}}
	hooks.Lookup = func(path string) (jobs.OwnedPlacement, error) { return owned[path], nil }
	hooks.Begin = func(_, path, digest string) error {
		p := owned[path]
		p.Path = path
		p.Pending = digest
		owned[path] = p
		return nil
	}
	hooks.Commit = func(path, digest string) error {
		owned[path] = jobs.OwnedPlacement{Path: path, Digest: digest}
		return nil
	}
	hooks.Owned = func() ([]jobs.OwnedPlacement, error) {
		out := []jobs.OwnedPlacement{}
		for _, p := range owned {
			if p.Digest != "" {
				out = append(out, p)
			}
		}
		return out, nil
	}
	hooks.Retired = func(path string) error { delete(owned, path); return nil }
	return hooks, owned
}

func TestCanceledPublicationDoesNotPublishTemporaryCopy(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "source.epub"), filepath.Join(dir, "destination.epub")
	writeTestEPUB(t, src, "valid book")
	ctx, cancel := context.WithCancel(context.Background())
	checks := 0
	hooks := &jobs.ImportHooks{Check: func() error {
		checks++
		if checks == 2 {
			cancel()
		}
		return ctx.Err()
	}}
	e := copyVerifiedContext(jobs.WithImportHooks(ctx, hooks), src, dst)
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if _, e = os.Stat(dst); !os.IsNotExist(e) {
		t.Fatal("canceled copy was published")
	}
	temps, _ := filepath.Glob(filepath.Join(dir, ".arrdio-import-*"))
	if len(temps) != 0 {
		t.Fatal("partial files retained", temps)
	}
}

func TestCorrectionReplacesOnlyOwnedUnmodifiedCopy(t *testing.T) {
	dir := t.TempDir()
	first, second, dst := filepath.Join(dir, "first.epub"), filepath.Join(dir, "second.epub"), filepath.Join(dir, "book.epub")
	writeTestEPUB(t, first, "first result")
	writeTestEPUB(t, second, "corrected result")
	hooks, owned := testOwnership()
	ctx := jobs.WithImportHooks(context.Background(), hooks)
	if e := copyVerifiedContext(ctx, first, dst); e != nil {
		t.Fatal(e)
	}
	firstHash, _ := digestFile(first)
	if e := copyVerifiedContext(ctx, second, dst); e == nil {
		t.Fatal("ordinary import overwrote published book")
	}
	hooks.Replace = true
	if e := copyVerifiedContext(ctx, second, dst); e != nil {
		t.Fatal(e)
	}
	got, _ := digestFile(dst)
	want, _ := digestFile(second)
	if got != want || owned[dst].Digest != want {
		t.Fatal("replacement was not recorded")
	}
	if hash, _ := digestFile(first); hash != firstHash {
		t.Fatal("source changed")
	}
	os.WriteFile(dst, []byte("external modification"), 0600)
	if e := copyVerifiedContext(ctx, first, dst); e == nil {
		t.Fatal("external modification overwritten")
	}
	content, _ := os.ReadFile(dst)
	if string(content) != "external modification" {
		t.Fatal("foreign bytes changed")
	}
}
func TestCorrectionRetiresOnlyPriorOwnedRepresentationFiles(t *testing.T) {
	root := t.TempDir()
	original := filepath.Join(root, "book.epub")
	writeTestEPUB(t, original, "original")
	replacement := filepath.Join(root, "book.pdf")
	os.WriteFile(replacement, append([]byte("%PDF-1.4\n"), append(bytes.Repeat([]byte{' '}, 2048), []byte("\n%%EOF\n")...)...), 0600)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"total":1,"results":[{"id":"book","media":{"metadata":{"title":"A"},"ebookFile":{}}}]}`))
	}))
	defer server.Close()
	destination := t.TempDir()
	library := &Libraries{ABSURL: server.URL, ABSToken: "fixture", Routes: []Route{{Category: "books", Format: "ebook", Service: "audiobookshelf", LibraryID: "1", Path: destination}}}
	hooks, owned := testOwnership()
	hooks.Protected[original] = true
	hooks.Protected[replacement] = true
	ctx := jobs.WithImportHooks(context.Background(), hooks)
	if _, e := library.Import(ctx, jobs.Input{Title: "A"}, "ebook", []string{original}, ""); e != nil {
		t.Fatal(e)
	}
	var prior string
	for path := range owned {
		prior = path
	}
	unrelated := filepath.Join(filepath.Dir(prior), "unrelated.txt")
	os.WriteFile(unrelated, []byte("keep"), 0600)
	hooks.Replace = true
	if _, e := library.Import(ctx, jobs.Input{Title: "A"}, "ebook", []string{replacement}, ""); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(prior); !os.IsNotExist(e) {
		t.Fatal("obsolete owned copy retained")
	}
	for _, path := range []string{original, replacement, unrelated} {
		if _, e := os.Stat(path); e != nil {
			t.Fatal("source or unrelated file removed", path)
		}
	}
	if len(owned) != 1 {
		t.Fatal(owned)
	}
}

func TestKavitaImportRenewsExpiredSharedTokenAndRefreshes(t *testing.T) {
	var expired atomic.Bool
	var scans, auths atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/Plugin/authenticate" {
			auths.Add(1)
			w.Write([]byte(`{"token":"new"}`))
			return
		}
		if expired.Load() && r.Header.Get("Authorization") != "Bearer new" {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/Series/v2":
			w.Header().Set("Pagination", `{"totalPages":1}`)
			w.Write([]byte(`[{"id":1,"name":"A"}]`))
		case "/api/Series/metadata":
			w.Write([]byte(`{"writers":[{"name":"Writer"}]}`))
		case "/api/Series/volumes":
			w.Write([]byte(`[{"name":"1","chapters":[{"number":"1","files":[{"filePath":"/books/A/book.epub"}]}]}]`))
		case "/api/Library/scan":
			scans.Add(1)
			w.WriteHeader(200)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	root := t.TempDir()
	src := filepath.Join(root, "book.epub")
	writeTestEPUB(t, src, "A")
	library := &Libraries{KavitaURL: server.URL, KavitaToken: "old", KavitaKey: "fixture", Routes: []Route{{Category: "books", Format: "ebook", Service: "kavita", LibraryID: "1", Path: t.TempDir()}}}
	in := jobs.Input{Title: "A", Author: "Writer"}
	if _, e := library.Presence(context.Background(), in, "ebook"); e != nil {
		t.Fatal(e)
	}
	expired.Store(true)
	library.Import(context.Background(), in, "ebook", []string{src}, "")
	presence, e := library.Import(context.Background(), in, "ebook", []string{src}, "")
	if e != nil || presence[0].State != "available" || auths.Load() != 1 || scans.Load() < 1 {
		t.Fatal(presence, e, auths.Load(), scans.Load())
	}
}

func TestKavitaMixedFormatCacheDoesNotHideExistingFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/Series/v2":
			w.Header().Set("Pagination", `{"totalPages":1}`)
			w.Write([]byte(`[{"id":1,"name":"A"}]`))
		case "/api/Series/volumes":
			w.Write([]byte(`[{"name":"1","chapters":[{"number":"1","files":[{"filePath":"/books/A/book.pdf"}]}]}]`))
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	root := t.TempDir()
	library := &Libraries{KavitaURL: server.URL, KavitaToken: "fixture", Routes: []Route{{Category: "ebook", Format: "ebook", Service: "kavita", LibraryID: "1", Path: root}, {Category: "manga", Format: "manga", Service: "kavita", LibraryID: "1", Path: root}}}
	for _, format := range []string{"ebook", "manga"} {
		presence, e := library.Presence(context.Background(), jobs.Input{Title: "A"}, format)
		if e != nil || presence[0].State != "available" {
			t.Fatal(format, presence, e)
		}
	}
}

func TestImportRequestsScanWhenWatcherIsDisabled(t *testing.T) {
	var indexed atomic.Bool
	var scans atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/api/libraries/1/scan" {
			indexed.Store(true)
			scans.Add(1)
			w.WriteHeader(200)
			return
		}
		if indexed.Load() {
			w.Write([]byte(`{"total":1,"results":[{"id":"book","media":{"metadata":{"title":"A"},"ebookFile":{}}}]}`))
		} else {
			w.Write([]byte(`{"total":0,"results":[]}`))
		}
	}))
	defer server.Close()
	root := t.TempDir()
	src := filepath.Join(root, "book.epub")
	writeTestEPUB(t, src, "A")
	library := &Libraries{ABSURL: server.URL, ABSToken: "fixture", Routes: []Route{{Category: "books", Format: "ebook", Service: "audiobookshelf", LibraryID: "1", Path: t.TempDir()}}}
	presence, e := library.Import(context.Background(), jobs.Input{Title: "A"}, "ebook", []string{src}, "")
	if e != nil || scans.Load() != 1 || presence[0].State != "available" {
		t.Fatal(presence, e, scans.Load())
	}
}

func TestTerminalPeerFailureRetainsAndReusesCompletedTrack(t *testing.T) {
	if _, e := exec.LookPath("ffmpeg"); e != nil {
		t.Skip("ffmpeg unavailable")
	}
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "Book"), 0700)
	first := filepath.Join(root, "Book", "01 chapter.mp3")
	if e := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=duration=0.2", "-c:a", "libmp3lame", first).Run(); e != nil {
		t.Fatal(e)
	}
	info, _ := os.Stat(first)
	set := peerSet{Username: "old-peer", Folder: "Book", Files: []peerFile{{Filename: `Book\01 chapter.mp3`, Size: info.Size()}, {Filename: `Book\02 chapter.mp3`, Size: 100}}}
	submittedCh := make(chan []peerFile, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			var files []peerFile
			json.NewDecoder(r.Body).Decode(&files)
			submittedCh <- files
			w.Write([]byte(`[]`))
			return
		}
		json.NewEncoder(w).Encode([]any{map[string]any{"username": "old-peer", "directories": []any{map[string]any{"files": []peerFile{{Filename: set.Files[0].Filename, State: "Completed, Succeeded"}, {Filename: set.Files[1].Filename, State: "Completed, Errored"}}}}}})
	}))
	defer server.Close()
	source := &Soulseek{URL: server.URL, Key: "fixture", DownloadRoot: root}
	data, _ := json.Marshal(set)
	receipt, e := source.Reconcile(context.Background(), jobs.Receipt{ID: string(data), State: "provider_wait"})
	var retry jobs.RetryTransferFailure
	if !errors.As(e, &retry) || len(receipt.Completed) != 1 {
		t.Fatal(receipt, e)
	}
	set.Username = "new-peer"
	data, _ = json.Marshal(set)
	ctx := jobs.WithRetainedFiles(context.Background(), receipt.Completed)
	ctx = jobs.WithTransferCheckpoint(ctx, func(jobs.Receipt) error { return nil })
	_, e = source.Acquire(ctx, jobs.Candidate{Extra: data}, root)
	if e != nil {
		t.Fatal(e)
	}
	submitted := <-submittedCh
	if len(submitted) != 1 || submitted[0].Filename != set.Files[1].Filename {
		t.Fatal("completed file requested again", submitted)
	}
	if _, e = os.Stat(first); e != nil {
		t.Fatal("completed file lost")
	}
}
func TestSharedPackRepairsOnlyRequestedMissingPriorities(t *testing.T) {
	calls := make(chan url.Values, 4)
	var priority atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			w.Write([]byte(`[{"progress":0.5,"state":"uploading","tags":"other-owner"}]`))
		case "/api/v2/torrents/files":
			json.NewEncoder(w).Encode([]any{map[string]any{"index": 0, "name": "Series v01-16/Series v01.cbz", "priority": 7, "progress": 1, "size": 10}, map[string]any{"index": 1, "name": "Series v01-16/Series v02.cbz", "priority": priority.Load(), "progress": 0, "size": 10}})
		case "/api/v2/torrents/filePrio":
			r.ParseForm()
			calls <- r.PostForm
			priority.Store(1)
			w.WriteHeader(200)
		default:
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	torrent := &Torrent{URL: server.URL, DownloadRoot: t.TempDir()}
	receipt := jobs.Receipt{ID: "0123456789abcdef0123456789abcdef01234567", State: "transferring", Volume: "2", Format: "manga", SelectionDone: true, OwnerTag: "our-owner"}
	next, e := torrent.Poll(context.Background(), receipt)
	if e != nil {
		t.Fatal(e)
	}
	change := <-calls
	if change.Get("id") != "1" || change.Get("priority") != "1" {
		t.Fatal("existing selection modified", change)
	}
	if _, e = torrent.Poll(context.Background(), next); e != nil {
		t.Fatal(e)
	}
	if len(calls) != 0 {
		t.Fatal("already selected files rewritten")
	}
}
