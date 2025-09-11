package prowlarr

import (
	"github.com/go-chi/chi/v5"

	"net/http"
)

func Router() chi.Router {
	r := chi.NewRouter()

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Accessing Prowlarr Endpoint\n"))
	})

	// /audiobookshelf/getbook

	// /audiobookshelf/getlistbook
	// r.Get("/getlistbook", GetListBook)

	// add more routes or middleware (r.Use) as you wish
	return r
}
