package abb

import (
	"github.com/go-chi/chi/v5"

	"net/http"
)

func Router() chi.Router {
	r := chi.NewRouter()

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Accessing AudioBookBay Endpoint\n"))
	})

	r.Post("/searchbooks", SearchBooks)

	return r
}
