package zlib

import (
	"fmt"

	"github.com/go-chi/chi/v5"

	"net/http"
)

func Router() chi.Router {
	r := chi.NewRouter()

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Accessing Zlib Endpoint\n"))
	})

	// r.Get("/test", Test)

	r.Put("/putcategory", PutCategory)

	r.Put("/putzlibuser", PutZLibUser)

	r.Get("/getzlibuser", GetZLibUser)

	r.Get("/getcategory", GetCateory)

	r.Post("/searchbook", SearchBook)

	r.Post("/downloadbook", DownloadBook)

	r.Get("/refreshusers", func(w http.ResponseWriter, r *http.Request) {
		err := RefreshZLibUser()

		if err != nil {
			fmt.Printf("%v\n", err)
		}
	})

	r.Delete("/deleteuser/{email}", DeleteUser)

	r.Delete("/deletecategory/{category}", DeleteCategory)
	return r
}
