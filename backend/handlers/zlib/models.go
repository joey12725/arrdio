package zlib

import "github.com/ashkaty/arrdio-api/services/models"

type DownloadItem struct {
	BookItem models.EBook `json:"book"`
	Category string
}

type DownloadRequest struct {
	Request []DownloadItem `json:"request"`
}

type DownloadResult struct {
	BookItem models.EBook `json:"book,omitempty"`
	Error    string       `json:"error,omitempty"`
}

type DownloadResponse struct {
	Warning  string           `json:"warning,omitempty"`
	Response []DownloadResult `json:"response"`
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
