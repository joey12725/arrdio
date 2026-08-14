package models

type BookRequest struct {
	Title  string `json:"title"`
	Author string `json:"author"`
}

type RequestItem struct {
	Book     BookRequest `json:"book"`
	Category string      `json:"category"`
}

type Request struct {
	Request []RequestItem `json:"request"`
}

type EBook struct {
	Title     string `json:"title"`
	Author    string `json:"author"`
	Year      int    `json:"year"`
	ImageLink string `json:"cover"`
	ID        int    `json:"id"`
	Extension string `json:"extension"`
	Hash      string `json:"hash"`
	Source    string `json:"source"`
}

type AudioBook struct {
	ImageLink  string `json:"image_link"`
	HrefLink   string `json:"href_link,omitempty"`
	MagnetLink string `json:"magnet_link"`
	Title      string `json:"title"`
	Author     string `json:"author,omitempty"`
	Source     string `json:"source"`
}

type AudiobookResultItem struct {
	Title     string     `json:"title"`
	AudioBook *AudioBook `json:"book,omitempty"`
	Error     string     `json:"error,omitempty"`
}

type EbookResultItem struct {
	Title string `json:"title"`
	EBook *EBook `json:"book,omitempty"`
	Error string `json:"error,omitempty"`
}

type AudiobookResult struct {
	Result []AudiobookResultItem `json:"result"`
}

type EbookResult struct {
	EbookResult []EbookResultItem `json:"result"`
	Warning     string            `json:"warning,omitempty"`
}
