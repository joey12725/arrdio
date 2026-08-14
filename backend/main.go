package main

import (
	_ "github.com/ashkaty/arrdio-api/docs" // swagger docs
	"fmt"
	"log"
	"net/http"

	abb "github.com/ashkaty/arrdio-api/handlers/abb"
	"github.com/ashkaty/arrdio-api/handlers/openlibrary"
	prowlarr "github.com/ashkaty/arrdio-api/handlers/prowlarr"
	qbit "github.com/ashkaty/arrdio-api/handlers/qbit"
	zlib "github.com/ashkaty/arrdio-api/handlers/zlib"
	db "github.com/ashkaty/arrdio-api/services/db"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	httpSwagger "github.com/swaggo/http-swagger"
	"github.com/playwright-community/playwright-go"
)

// @title Arrdio API
// @version 1.0
// @description API for searching and downloading books and audiobooks from various sources
// @host localhost:2130
// @BasePath /
func main() {

	err := db.Init()

	if err != nil {
		log.Fatalf("error intializing browser: %v", err.Error())
	}

	r := chi.NewRouter()

	// Apply middleware
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.URLFormat)

	fmt.Println("Checking if Playwright is installed")
	if err := playwright.Install(); err != nil {
		log.Fatalf("failed to install browsers: %v", err)
	}

	// Define routes
	fmt.Println("Starting API Server")
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("Hello, Chi!\n"))
	})
	r.Get("/swagger/*", httpSwagger.WrapHandler)

	r.Mount("/abb", abb.Router())
	r.Mount("/qbit", qbit.Router())

	r.Mount("/zlib", zlib.Router())
	r.Mount("/openlibrary", openlibrary.Router())
	r.Mount("/prowlarr", prowlarr.Router())

	// Start the server
	http.ListenAndServe(":2130", r)
}
