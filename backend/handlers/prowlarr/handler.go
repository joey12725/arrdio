package prowlarr

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ashkaty/arrdio-api/handlers/openlibrary"
	"github.com/ashkaty/arrdio-api/services/models"
	"golift.io/starr"
	"golift.io/starr/prowlarr"
)

func getClient() (*prowlarr.Prowlarr, error) {
	c := starr.New("REDACTED_PROWLARR_KEY", "https://prowlarr.example.com/", 30*time.Second)
	p := prowlarr.New(c)
	if err := p.Ping(); err != nil {
		return nil, fmt.Errorf("prowlarr not reachable: %w", err)
	}
	return p, nil
}

// isAudiobook checks if the result is an audiobook based on file extensions and keywords
func isAudiobook(title string) bool {
	// Common audiobook file extensions
	audiobookExtensions := []string{".m4b", ".mp3", ".m4a", ".aac", ".flac", ".ogg", ".opus"}

	// Common ebook extensions to exclude
	ebookExtensions := []string{".epub", ".pdf", ".mobi", ".azw3", ".azw", ".djvu", ".fb2", ".cbr", ".cbz"}

	titleLower := strings.ToLower(title)

	// Exclude if it contains ebook extensions
	for _, ext := range ebookExtensions {
		if strings.Contains(titleLower, ext) {
			return false
		}
	}

	// Include if it contains audiobook extensions
	for _, ext := range audiobookExtensions {
		if strings.Contains(titleLower, ext) {
			return true
		}
	}

	// Also check for "audiobook" keyword in title
	if strings.Contains(titleLower, "audiobook") || strings.Contains(titleLower, "audio book") {
		return true
	}

	// Default to true if no ebook extensions found (category filter should handle most filtering)
	return true
}

// SearchBook handles audiobook searches via Prowlarr
// @Summary Search audiobooks via Prowlarr
// @Tags Prowlarr
// @Accept json
// @Produce json
// @Param request body models.Request true "Search request"
// @Success 200 {object} models.AudiobookResult
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Server error"
// @Router /prowlarr/searchbooks [post]
func SearchBook(w http.ResponseWriter, r *http.Request) {
	var req models.Request

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Request) == 0 {
		http.Error(w, "request must contain at least one entry", http.StatusBadRequest)
		return
	}

	prowlarrClient, err := getClient()
	if err != nil {
		http.Error(w, "unable to connect to prowlarr: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Process all books in parallel
	results := make([]models.AudiobookResultItem, len(req.Request))
	var wg sync.WaitGroup
	wg.Add(len(req.Request))

	//test input if indexer works before going into for loop
	testInput := prowlarr.SearchInput{
		Query:      "test",
		IndexerIDs: []int64{18},   // MyAnonMouse
		Categories: []int64{3000}, // Audiobooks category
	}

	_, err = prowlarrClient.SearchContext(context.Background(), testInput)
	if err != nil {
		http.Error(w, "error with prowlarr search: "+err.Error(), http.StatusInternalServerError)
		return
	}
	for i, book := range req.Request {
		i, book := i, book
		go func() {
			defer wg.Done()

			// Search using MyAnonMouse tracker (indexer ID 13)
			// Categories: 3000 = Audiobooks on MyAnonMouse
			input := prowlarr.SearchInput{
				Query:      book.Book.Title,
				IndexerIDs: []int64{18},   // MyAnonMouse
				Categories: []int64{3000}, // Audiobooks category
			}

			searchResults, err := prowlarrClient.SearchContext(context.Background(), input)
			if err != nil {
				http.Error(w, "error with prowlarr search: "+err.Error(), http.StatusInternalServerError)
				return
			}

			// Filter results to only include audiobooks (by file extension)
			var prowlarrResults []*prowlarr.Search
			for _, result := range searchResults {
				// Check if it's an audiobook based on common audiobook extensions
				if isAudiobook(result.Title) {
					prowlarrResults = append(prowlarrResults, result)
				}
			}

			// Check if any audiobooks were found
			if len(prowlarrResults) == 0 {
				results[i] = models.AudiobookResultItem{
					Title: book.Book.Title,
					Error: "error book not found",
				}
				return
			}

			// Get the first audiobook result (best match)
			firstResult := prowlarrResults[0]

			imageLink := ""
			olResult, err := openlibrary.SearchBook(firstResult.Title)

			if err == nil {
				imageLink = olResult.CoverURL
			}

			results[i] = models.AudiobookResultItem{
				Title: book.Book.Title,
				AudioBook: &models.AudioBook{
					ImageLink:  imageLink,
					MagnetLink: firstResult.DownloadURL,
					Title:      firstResult.Title,
					Source:     fmt.Sprintf("%s (Prowlarr)", firstResult.Indexer),
				},
			}
		}()
	}

	// Wait for all searches to complete
	wg.Wait()

	// Return all results as JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(models.AudiobookResult{Result: results})
}

func GrabBook() {}
