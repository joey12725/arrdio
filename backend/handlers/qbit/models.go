package qbit

type Category struct {
	Name     string `json:"name"`
	SavePath string `json:"savePath"`
}

type TorrentItem struct {
	Magnet   string `json:"magnet"`
	Category string `json:"category,omitempty"`
}

type TorrentRequest struct {
	Request []TorrentItem `json:"request"`
}
type TorrentResult struct {
	Magnet string `json:"magnet"`
	Error  string `json:"error,omitempty"`
}

type TorrentResponse struct {
	Results []TorrentResult `json:"response"`
}
