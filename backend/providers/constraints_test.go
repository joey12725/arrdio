package providers

import (
	"context"
	"github.com/ashkaty/arrdio-api/jobs"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestKnownLibraryMismatchIsAbsentNotUnknown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"total":1,"results":[{"id":"french","media":{"metadata":{"title":"A","language":"fr"},"ebookFile":{"metadata":{"ext":"epub"}}}}]}`))
	}))
	defer server.Close()
	library := &Libraries{ABSURL: server.URL, ABSToken: "fixture", Routes: []Route{{Format: "ebook", Service: "audiobookshelf", LibraryID: "1"}}}
	presence, e := library.Presence(context.Background(), jobs.Input{Title: "A", Constraints: jobs.Constraints{Language: "English"}}, "ebook")
	if e != nil || presence[0].State != "absent" {
		t.Fatal(presence, e)
	}
}
func TestKavitaChecksAllContainersBeforeReportingMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/Series/v2":
			w.Header().Set("Pagination", `{"totalPages":1}`)
			w.Write([]byte(`[{"id":1,"name":"A"}]`))
		case "/api/Series/metadata":
			w.Write([]byte(`{"language":"en"}`))
		case "/api/Series/volumes":
			w.Write([]byte(`[{"name":"1","chapters":[{"files":[{"filePath":"/books/A/book.pdf"},{"filePath":"/books/A/book.epub"}]}]}]`))
		case "/api/Search/search":
			w.Write([]byte(`{"series":[],"chapters":[]}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	library := &Libraries{KavitaURL: server.URL, KavitaToken: "fixture", Routes: []Route{{Format: "ebook", Service: "kavita", LibraryID: "1"}}}
	presence, e := library.Presence(context.Background(), jobs.Input{Title: "A", Constraints: jobs.Constraints{Language: "English", Container: "epub"}}, "ebook")
	if e != nil || presence[0].State != "available" {
		t.Fatal(presence, e)
	}
	presence, e = library.Presence(context.Background(), jobs.Input{Title: "A", Constraints: jobs.Constraints{Language: "French"}}, "ebook")
	if e != nil || presence[0].State != "absent" {
		t.Fatal(presence, e)
	}
}
