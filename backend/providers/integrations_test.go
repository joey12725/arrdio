package providers

import (
	"context"
	"encoding/json"
	"github.com/ashkaty/arrdio-api/jobs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

func TestOpenRouterDecisionsValidateSchemaAndDoNotCacheFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("invalid router endpoint/auth")
		}
		var input map[string]any
		if json.NewDecoder(r.Body).Decode(&input) != nil || input["model"] != "typesafe/jev-router" || input["max_tokens"] != float64(64) {
			t.Error("unexpected model request")
		}
		w.Header().Set("Content-Type", "application/json")
		content := `{"index":1}`
		if call == 1 {
			content = `{"index":99}`
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "effective-fixture-model", "choices": []any{map[string]any{"finish_reason": "stop", "message": map[string]string{"content": content}}}})
	}))
	defer server.Close()
	model := &Models{RouterURL: server.URL, RouterKey: "fixture", RouterModel: "typesafe/jev-router"}
	in := jobs.Input{Title: "A"}
	candidates := []jobs.Candidate{{ID: "first"}, {ID: "second"}}
	if choice := model.Decide(context.Background(), in, candidates); choice != 0 || !model.Degraded {
		t.Fatal("invalid index accepted", choice, model.Degraded)
	}
	if choice := model.Decide(context.Background(), in, candidates); choice != 1 || model.Degraded {
		t.Fatal("failed request poisoned cache", choice, model.Degraded)
	}
	if choice := model.Decide(context.Background(), in, candidates); choice != 1 || calls.Load() != 2 {
		t.Fatal("stable successful judgment not cached", choice, calls.Load())
	}
	if !strings.Contains(model.Status()["typesafe"], "effective-fixture-model") {
		t.Fatal(model.Status())
	}
}

func TestABBAuthenticatedSearchUsesRealTitleQueryAndParsedMetadata(t *testing.T) {
	var logins atomic.Int32
	var details atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		if r.Header.Get("User-Agent") == "" {
			t.Error("ABB user agent missing")
		}
		switch r.URL.Path {
		case "/member/login.php":
			if r.Method == "POST" {
				logins.Add(1)
				r.ParseForm()
				if r.Form.Get("username") != "fixture-user" || r.Form.Get("password") != "fixture-password" {
					t.Error("ABB credential fields incorrect")
				}
				http.SetCookie(w, &http.Cookie{Name: "member", Value: "fixture", Path: "/"})
				http.Redirect(w, r, "/member/users/", 302)
				return
			}
			w.Write([]byte(`<form><input name="username"><input name="password"></form>`))
		case "/member/users/":
			if _, e := r.Cookie("member"); e != nil {
				w.Write([]byte(`<form><input name="password"></form>`))
				return
			}
			w.Write([]byte(`<a href="/member/logout.php">Logout</a>`))
		case "/":
			if r.URL.Query().Get("s") != "the book of life" || r.URL.Query().Get("tt") != "1" {
				t.Error("ABB search did not use the supported lowercase title query")
			}
			w.Write([]byte(`<a href="/abss/book">Book</a><a href="/abss/book">Duplicate</a>`))
		case "/abss/book":
			details.Add(1)
			if _, e := r.Cookie("member"); e != nil {
				t.Error("member cookie not preserved")
			}
			w.Write([]byte(`<title>The Book of Life - Upton Sinclair Audiobook M4B</title><p>Language: English Keywords: Science Shared by: fixture Written by Upton Sinclair Format: M4B Bitrate: 128 Kbps Unabridged</p><table><tr><td>Info Hash:</td><td>0123456789abcdef0123456789abcdef01234567</td></tr></table>`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	abb := &ABB{URL: server.URL, Username: "fixture-user", Password: "fixture-password"}
	for i := 0; i < 2; i++ {
		results, e := abb.Search(context.Background(), jobs.Input{Title: "The Book of Life", Author: "Upton Sinclair"}, "audio")
		if e != nil || len(results) != 1 {
			t.Fatal(results, e)
		}
		c := results[0]
		if c.Author != "Upton Sinclair" || c.Language != "English" || c.Extension != "m4b" || c.Edition != "unabridged" {
			t.Fatal(c)
		}
	}
	// One login, and the parsed detail page is reused by the second search.
	if logins.Load() != 1 || details.Load() != 1 {
		t.Fatal("session not reused or detail page refetched", logins.Load(), details.Load())
	}
}

func TestABBRejectsUnverifiedLoginAndCrossOriginRedirect(t *testing.T) {
	receiverCalls := atomic.Int32{}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { receiverCalls.Add(1) }))
	defer receiver.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			http.Redirect(w, r, receiver.URL, 307)
			return
		}
		w.Write([]byte(`<form><input name="password"></form>`))
	}))
	defer server.Close()
	abb := &ABB{URL: server.URL, Username: "fixture", Password: "fixture"}
	if e := abb.VerifyLogin(context.Background()); e == nil {
		t.Fatal("redirected login accepted")
	}
	if receiverCalls.Load() != 0 {
		t.Fatal("credentials forwarded to a different origin")
	}
}

func TestShortTitlesDoNotAcceptDifferentBookInSameSeries(t *testing.T) {
	in := jobs.Input{Title: "Dracula", Author: "Bram Stoker"}
	candidates := Rank(in, "audio", []jobs.Candidate{{Title: "Children of Dracula - Bram Stoker (M4B)", Format: "audio"}, {Title: "Bram Stoker - Dracula (LibriVox) [m4b]", Format: "audio"}, {Title: "Dracula Messiah - Bram Stoker", Format: "audio"}})
	if len(candidates) != 1 || !strings.Contains(candidates[0].Title, "LibriVox") {
		t.Fatal(candidates)
	}
}
func TestABBInitialDetailLinksCannotDowngradeOrCarryUserinfo(t *testing.T) {
	base, _ := url.Parse("https://abb.example.invalid")
	for _, href := range []string{"http://abb.example.invalid/abss/book", "/abss/book"} {
		detail, e := abbDetailURL(base, href)
		if e != nil || detail.Scheme != "https" || detail.User != nil {
			t.Fatal(href, detail, e)
		}
	}
	for _, href := range []string{"https://user:password@abb.example.invalid/abss/book", "http://other.example.invalid/abss/book", "ftp://abb.example.invalid/abss/book", "https://abb.example.invalid/member/logout.php"} {
		if _, e := abbDetailURL(base, href); e == nil {
			t.Fatal("untrusted initial detail request allowed", href)
		}
	}
}
func TestShortTitleWithParsedAuthorDoesNotRequireAuthorInput(t *testing.T) {
	results := Rank(jobs.Input{Title: "Dracula"}, "audio", []jobs.Candidate{{Title: "Dracula - Bram Stoker", Author: "Bram Stoker", Format: "audio"}, {Title: "Children of Dracula - Bram Stoker", Author: "Bram Stoker", Format: "audio"}})
	if len(results) != 1 || results[0].Title != "Dracula - Bram Stoker" {
		t.Fatal(results)
	}
}
func TestSoulseekFiltersWrongWorkBeforeSpendingDirectoryBudget(t *testing.T) {
	var inspected atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v0/searches":
			w.Write([]byte(`{"id":"test-search"}`))
		case "/api/v0/searches/test-search":
			w.Write([]byte(`{"state":"Completed, TimedOut"}`))
		case "/api/v0/searches/test-search/responses":
			responses := []any{}
			for i := 0; i < 8; i++ {
				responses = append(responses, map[string]any{"username": "wrong-peer", "hasFreeUploadSlot": true, "queueLength": i, "files": []peerFile{{Filename: "Books/Children of Dracula/book.m4b", Size: 100}}})
			}
			responses = append(responses, map[string]any{"username": "right-peer", "hasFreeUploadSlot": true, "queueLength": 8, "files": []peerFile{{Filename: "Books/Dracula/book.m4b", Size: 100}}})
			json.NewEncoder(w).Encode(responses)
		case "/api/v0/users/right-peer/directory":
			inspected.Add(1)
			w.Write([]byte(`[{"name":"Books/Dracula","files":[{"filename":"book.m4b","size":100}]}]`))
		default:
			t.Error("irrelevant folder was browsed", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	source := &Soulseek{URL: server.URL, Key: "fixture"}
	results, e := source.Search(context.Background(), jobs.Input{Title: "Dracula"}, "audio")
	if e != nil || len(results) != 1 || results[0].Title != "Dracula" || inspected.Load() != 1 {
		t.Fatal(results, e, inspected.Load())
	}
}
