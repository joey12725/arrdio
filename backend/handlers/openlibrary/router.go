package openlibrary

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func Router() chi.Router {
	r := chi.NewRouter()

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Accessing Open Library Endpoint\n"))
	})

	r.Post("/search", SearchBookHandler)

	return r
}
