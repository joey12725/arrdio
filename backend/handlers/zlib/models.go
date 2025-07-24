package zlib

type DownloadItem struct {
	BookItem EBook `json:"book"`
	Category string
}

type DownloadRequest struct {
	Request []DownloadItem `json:"request"`
}

type DownloadResult struct {
	BookItem EBook  `json:"book,omitempty"`
	Error    string `json:"error,omitempty"`
}

type DownloadResponse struct {
	Warning  string           `json:"warning,omitempty"`
	Response []DownloadResult `json:"response"`
}

type Book struct {
	Title  string `json:"title"`
	Author string `json:"author,omitempty"`
}

type SearchItem struct {
	BookItem Book `json:"book"`
	Category string
}
type SearchRequest struct {
	Request []SearchItem `json:"request"`
}

type SearchResult struct {
	Books *EBook `json:"book"`
	Error error  `json:"error,omitempty"`
	Title string `json:"title,omitempty"`
}

type SearchResponse struct {
	Response []SearchResult `json:"response"`
}
type CategoryRequest struct {
	Category string `json:"category"`
	SavePath string `json:"savepath"`
}

type CategoryResponse struct {
	ID       int64  `json:"id"`
	Category string `json:"category"`
	SavePath string `json:"savepath"`
}

type ZLibUserRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type ZLibUser struct {
	ID             int64  `json:"id"`
	Email          string `json:"email"`
	Download_Count int    `json:"download_count"`
	Password       string `json:"password,omitempty"`
	User_ID        string `json:"user_id,omitempty"`
	User_Key       string `json:"user_key,omitempty"`
}
