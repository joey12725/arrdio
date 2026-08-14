package openlibrary

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
)

// Book represents a simplified book structure for requests

// SearchBook makes an API call to Open Library to search for a book
// Returns the cover URL, full title, and author information
func SearchBook(bookName string) (*BookResult, error) {
	if bookName == "" {
		return nil, fmt.Errorf("book name cannot be empty")
	}

	// Build the search URL with title-specific search for better accuracy
	baseURL := "https://openlibrary.org/search.json"
	params := url.Values{}
	params.Add("title", bookName)                                             // Use title parameter for more accurate matching
	params.Add("limit", "1")                                                  // Get only the top result
	params.Add("fields", "title,author_name,cover_i,first_publish_year,isbn") // Only get needed fields

	searchURL := fmt.Sprintf("%s?%s", baseURL, params.Encode())

	// Make the HTTP request
	resp, err := http.Get(searchURL)
	if err != nil {
		return nil, fmt.Errorf("failed to make request to Open Library: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("open Library returned status %d", resp.StatusCode)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Parse the JSON response
	var openlibResponse OpenLibraryResponse
	if err := json.Unmarshal(body, &openlibResponse); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	// Check if any books were found
	if openlibResponse.NumFound == 0 || len(openlibResponse.Books) == 0 {
		return nil, fmt.Errorf("no books found for query: %s", bookName)
	}

	// Extract the first result
	book := openlibResponse.Books[0]
	result := &BookResult{
		Title:   book.Title,
		CoverID: book.Cover,
		Year:    book.FirstPublish,
	}

	// Get the author name (first author if multiple)
	if len(book.AuthorName) > 0 {
		result.Author = book.AuthorName[0]
	}

	// Build the cover URL if cover ID exists
	if book.Cover > 0 {
		result.CoverURL = fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", book.Cover)
	}

	// Get the first ISBN if available
	if len(book.ISBN) > 0 {
		result.ISBN = book.ISBN[0]
	}

	return result, nil
}

// SearchBookHandler is the HTTP handler for the /search endpoint
// Expects a JSON payload with a list of books to search
// @Summary Search books on Open Library
// @Tags OpenLibrary
// @Accept json
// @Produce json
// @Param request body SearchRequest true "Search request"
// @Success 200 {object} SearchResponse
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Server error"
// @Router /openlibrary/search [post]
func SearchBookHandler(w http.ResponseWriter, r *http.Request) {
	var req SearchRequest

	// Decode the JSON request body
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}

	// Validate the request
	if len(req.Request) == 0 {
		http.Error(w, "request must contain at least one book", http.StatusBadRequest)
		return
	}

	// Process all books in parallel
	results := make([]BookResult, len(req.Request))
	var wg sync.WaitGroup
	wg.Add(len(req.Request))

	for i, item := range req.Request {
		i, item := i, item
		go func() {
			defer wg.Done()

			// Search for the book
			result, err := SearchBook(item.BookItem.Title)
			if err != nil {
				// Store error result
				results[i] = BookResult{
					Title: item.BookItem.Title,
					Error: err.Error(),
				}
			} else {
				// Store successful result
				results[i] = *result
			}
		}()
	}

	// Wait for all searches to complete
	wg.Wait()

	// Return all results as JSON
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(SearchResponse{Results: results})
}
