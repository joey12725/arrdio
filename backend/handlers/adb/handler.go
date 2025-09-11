package abb

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/playwright-community/playwright-go"

	"context"
	"net/http"
)

type AudioBook struct {
	ImageLink  string `json:"image_link"`
	HrefLink   string `json:"href_link"`
	MagnetLink string `json:"magnet_link"`
	Title      string `json:"title"`
	Author     string `json:"author,omitempty"`
}

type Book struct {
	Title  string `json:"title"`
	Author string `json:"author"`
}
type DownloadItem struct {
	BookItem Book `json:"book"`
	Category string
}
type DownloadRequest struct {
	Request []DownloadItem `json:"request"`
}

// per-title result
type BooksResult struct {
	Title     string     `json:"title"`
	AudioBook *AudioBook `json:"book,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// response payload for /books
type BooksResponse struct {
	Results []BooksResult `json:"results"`
}

func SearchBooks(w http.ResponseWriter, r *http.Request) {
	var req DownloadRequest

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON payload: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Request) == 0 {
		http.Error(w, "request must contain at least one entry", http.StatusBadRequest)
		fmt.Println(req)
		return
	}

	clientCtx := r.Context()

	pw, err := playwright.Run()

	if err != nil {
		http.Error(w, "error running playwright: "+err.Error(), http.StatusInternalServerError)
		return
	}

	browser, err := pw.Firefox.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})

	if err != nil {
		http.Error(w, "error creating browser: "+err.Error(), http.StatusInternalServerError)
		return
	}

	browserCtx, err := browser.NewContext()
	if err != nil {
		http.Error(w, "error creating browser context: "+err.Error(), http.StatusInternalServerError)
		return

	}

	page, err := browserCtx.NewPage()

	if err != nil {
		http.Error(w, "error creating login page: "+err.Error(), http.StatusInternalServerError)
		return
	}

	err = login(page)

	if err != nil {
		http.Error(w, "error creating logging in: "+err.Error(), http.StatusInternalServerError)
		return
	}

	results := make([]BooksResult, len(req.Request))
	var wg sync.WaitGroup
	wg.Add(len(req.Request))

	for i, item := range req.Request {
		i, item := i, item
		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(clientCtx, 15*time.Second)
			defer cancel()
			book, err := FetchBook(ctx, browserCtx, item.BookItem.Title)
			if err != nil {
				fmt.Printf("%v not found\n", item.BookItem.Title)
				results[i] = BooksResult{Title: item.BookItem.Title, Error: err.Error(), AudioBook: &book}
			} else {
				results[i] = BooksResult{Title: item.BookItem.Title, AudioBook: &book}
			}
		}()
	}

	wg.Wait()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(BooksResponse{Results: results})
}

// TODO: abstract this code out to another file
func FetchBook(clientCtx context.Context, browserCtx playwright.BrowserContext, bookTitle string) (AudioBook, error) {

	page, err := searchBook(bookTitle, browserCtx)

	if err != nil {
		return AudioBook{Title: ""}, err
	}

	notFound, notFoundError := page.Locator("#content > h3").InnerText()

	if notFoundError == nil && notFound == "Not Found" {
		return AudioBook{Title: ""}, fmt.Errorf("error book not found")
	}
	post := page.Locator("div.post:nth-child(2)")

	book := AudioBook{}

	book.Title, _ = post.Locator(".postTitle a").InnerText()
	// fmt.Println("%v", bookTitle)

	book.HrefLink, _ = post.Locator(".postTitle a").GetAttribute("href")
	// fmt.Println("%v", hRefLink)

	book.ImageLink, _ = post.Locator(".postContent img").GetAttribute("src")

	// fmt.Println("%v \n", imgSrc)

	book.HrefLink = "https://audiobookbay.lu" + book.HrefLink

	magnetLink := getMagnetLink(page, book.HrefLink)
	book.MagnetLink = magnetLink

	// fmt.Println("%v", bookList)
	return book, nil

}

// TODO: add paralization
func login(page playwright.Page) error {
	page.Goto("https://audiobookbay.lu/member/login.php")
	page.Locator("form[name='entryform'] input[name='username']").Fill("REDACTED_ABB_USER")
	err := page.Locator("form[name='entryform'] input[name='password']").Fill("REDACTED_ABB_PASSWORD")
	if err != nil {
		return err
	}
	page.Locator("input.login-button:nth-child(1)").Click()
	return nil
}

func getMagnetLink(page playwright.Page, hrefLink string) string {
	page.Goto(hrefLink)

	page.Locator("#magnetLink").Click()
	page.WaitForSelector("#magnetIcon")
	magnetLink, err := page.Locator("#magnetIcon").GetAttribute("href")
	if err != nil {
		return ""
	}

	return magnetLink
}

func searchBook(bookTitle string, browserCtx playwright.BrowserContext) (playwright.Page, error) {

	page, err := browserCtx.NewPage()

	if _, err = page.Goto("https://audiobookbay.lu/member/advanced_search"); err != nil {
		return nil, err
	}

	page.Fill("form[name='asearchform'] input[name='s']", bookTitle)
	page.Uncheck("#tt2")
	page.Uncheck("#tt3")

	page.Locator("a.searchSubmit").Nth(1).Click()

	page.WaitForSelector("div.post")

	return page, nil
}
