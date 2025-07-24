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

	// /audiobookshelf/getbook
	r.Post("/searchbooks", SearchBooks)

	// /audiobookshelf/getlistbook
	// r.Get("/getlistbook", GetListBook)

	// add more routes or middleware (r.Use) as you wish
	return r
}
