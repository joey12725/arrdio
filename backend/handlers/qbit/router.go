package qbit

import (
	"github.com/go-chi/chi/v5"

	"net/http"
)

func Router() chi.Router {
	r := chi.NewRouter()

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Accessing Qbitt Endpoint\n"))
	})

	// /audiobookshelf/getbook
	r.Post("/addtorrents", AddTorrent)

	return r
}
