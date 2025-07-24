package main

import (
	"fmt"
	"log"
	"net/http"

	abb "github.com/ashkaty/arrdio-api/handlers/adb"
	qbit "github.com/ashkaty/arrdio-api/handlers/qbit"
	zlib "github.com/ashkaty/arrdio-api/handlers/zlib"
	db "github.com/ashkaty/arrdio-api/services"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/playwright-community/playwright-go"
)

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

	r.Mount("/abb", abb.Router())
	r.Mount("/qbit", qbit.Router())

	r.Mount("/zlib", zlib.Router())

	// Start the server
	http.ListenAndServe(":8080", r)
}
