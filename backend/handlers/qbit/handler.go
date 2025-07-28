package qbit

import (
	"context"

	qb "github.com/autobrr/go-qbittorrent"

	"net/http"

	"encoding/json"
)

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

// newClient creates a qbittorrent client connected to qbittorrent.example.com
func newClient() (*qb.Client, error) {
	client := qb.NewClient(qb.Config{
		Host:     "https://qbittorrent.example.com",
		Username: "admin",
		Password: "REDACTED_QBIT_PASSWORD",
	})

	ctx := context.Background()

	if err := client.LoginCtx(ctx); err != nil {
		return nil, err
	}
	return client, nil
}

// AddTorrent connects to the client and adds the given magnet link with category
func addTorrent(magnet, category string, client *qb.Client) error {
	ctx := context.Background()
	opts := map[string]string{
		"category": category,
	}
	if err := client.AddTorrentFromUrlCtx(ctx, magnet, opts); err != nil {
		return err
	}
	return nil
}

func getCategories(client *qb.Client) ([]Category, error) {

	ctx := context.Background()
	categories, err := client.GetCategoriesCtx(ctx)
	if err != nil {
		return nil, err
	}

	var categoryList []Category

	for _, category := range categories {
		categoryList = append(categoryList, Category{Name: category.Name, SavePath: category.SavePath})
	}

	return categoryList, nil
}

func AddTorrent(w http.ResponseWriter, r *http.Request) {
	var request TorrentRequest
	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "error with response header: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(request.Request) == 0 {
		http.Error(w, "error no magnets in response header", http.StatusBadRequest)
		return
	}

	qbitclient, err := newClient()

	if err != nil {
		http.Error(w, "error intializing qbittorrent client: "+err.Error(), http.StatusInternalServerError)
		return
	}

	var response TorrentResponse
	for _, torrent := range request.Request {
		err := addTorrent(torrent.Magnet, torrent.Category, qbitclient)
		if err != nil {
			response.Results = append(response.Results, TorrentResult{Magnet: torrent.Magnet, Error: err.Error()})
		} else {
			response.Results = append(response.Results, TorrentResult{Magnet: torrent.Magnet})
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}
