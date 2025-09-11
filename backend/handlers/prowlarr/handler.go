package prowlarr

import (
	"time"

	"golift.io/starr"
	"golift.io/starr/prowlarr"

	"fmt"

	"context"

	"encoding/json"
	"net/http"
)

type Book struct {
	Title  string `json:"title"`
	Author string `json:"author"`
}
type DownloadItem struct {
	BookItem Book `json:"book"`
	Category string
}
type DownloadRequest struct {
	Request []DownloadItem `json:"request"`
}

func getClient() (*prowlarr.Prowlarr, error) {
	c := starr.New("REDACTED_PROWLARR_KEY", "https://prowlarr.example.com/", 30*time.Second)
	p := prowlarr.New(c)
	if err := p.Ping(); err != nil {
		return nil, fmt.Errorf("Prowlarr not reachable: %w", err)
	}
	return p, nil
}

func SearchBook(w http.ResponseWriter, r *http.Request) {

	var req DownloadRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Request) == 0 {
		http.Error(w, "request must contain at least one entry", http.StatusBadRequest)
		fmt.Println(req)
		return
	}

	prowlarrClient, err := getClient()

	if err != nil {
		http.Error(w, "unable to connect to prowlarr: "+err.Error(), http.StatusInternalServerError)
	}
	for _, book := range req.Request {
		input := prowlarr.SearchInput{
			Query:      "Your Book Title or Author",
			IndexerIDs: []int64{13},
		}

		results, err := prowlarrClient.SearchContext(context.Background(), input)
		if err != nil {
			panic(err)
		}
	}

	for _, result := range results {
		fmt.Printf("Title: %s, Indexer: %s, DownloadURL: %s\n", result.Title, result.Indexer, result.DownloadURL)
	}
}

func GrabBook() {}
