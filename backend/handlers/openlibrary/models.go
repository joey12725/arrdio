package openlibrary

type Book struct {
	Title  string `json:"title"`
	Author string `json:"author,omitempty"`
}

// BookResult represents the result for a single book search
type BookResult struct {
	Title    string `json:"title"`
	Author   string `json:"author,omitempty"`
	CoverURL string `json:"cover_url,omitempty"`
	CoverID  int64  `json:"cover_id,omitempty"`
	Year     int    `json:"year,omitempty"`
	ISBN     string `json:"isbn,omitempty"`
	Error    string `json:"error,omitempty"`
}

// SearchItem represents a single book search item
type SearchItem struct {
	BookItem Book `json:"book"`
}

// SearchRequest represents the request payload for searching books
type SearchRequest struct {
	Request []SearchItem `json:"request"`
}

// SearchResponse represents the response payload for book searches
type SearchResponse struct {
	Results []BookResult `json:"results"`
}

type OpenLibraryResult struct {
	Title        string   `json:"title"`
	AuthorName   []string `json:"author_name"`
	Cover        int64    `json:"cover_i"`
	FirstPublish int      `json:"first_publish_year"`
	ISBN         []string `json:"isbn"`
	Key          string   `json:"key"`
}

// OpenLibraryResponse represents the full Open Library API response
type OpenLibraryResponse struct {
	NumFound int                 `json:"numFound"`
	Start    int                 `json:"start"`
	Books    []OpenLibraryResult `json:"docs"`
}
