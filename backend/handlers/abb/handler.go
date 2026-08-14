package abb

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/ashkaty/arrdio-api/services/models"
	"github.com/playwright-community/playwright-go"

	"context"
	"net/http"
)

// SearchBooks handles audiobook searches on Audiobookbay
// @Summary Search audiobooks on Audiobookbay
// @Tags ABB
// @Accept json
// @Produce json
// @Param request body models.Request true "Search request"
// @Success 200 {object} models.AudiobookResult
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Server error"
// @Router /abb/searchbooks [post]
func SearchBooks(w http.ResponseWriter, r *http.Request) {
	var req models.Request

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

	results := make([]models.AudiobookResultItem, len(req.Request))
	var wg sync.WaitGroup
	wg.Add(len(req.Request))

	for i, item := range req.Request {
		i, item := i, item
		go func() {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(clientCtx, 15*time.Second)
			defer cancel()
			// fmt.Printf("Book Request: %s", item)
			book, err := FetchBook(ctx, browserCtx, item.Book.Title)
			if err != nil {
				fmt.Printf("%v not found\n", item.Book.Title)
				results[i] = models.AudiobookResultItem{Title: item.Book.Title, Error: err.Error(), AudioBook: &book}
			} else {
				results[i] = models.AudiobookResultItem{Title: item.Book.Title, AudioBook: &book}
			}
		}()
	}

	wg.Wait()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(models.AudiobookResult{Result: results})
}

// TODO: abstract this code out to another file
func FetchBook(clientCtx context.Context, browserCtx playwright.BrowserContext, bookTitle string) (models.AudioBook, error) {

	page, err := searchBook(bookTitle, browserCtx)

	if err != nil {
		return models.AudioBook{Title: ""}, err
	}

	notFound, notFoundError := page.Locator("#content > h3").InnerText()

	if notFoundError == nil && notFound == "Not Found" {
		return models.AudioBook{Title: ""}, fmt.Errorf("error book not found")
	}
	post := page.Locator("div.post:nth-child(2)")

	book := models.AudioBook{}

	book.Title, _ = post.Locator(".postTitle a").InnerText()
	// fmt.Println("%v", bookTitle)

	book.HrefLink, _ = post.Locator(".postTitle a").GetAttribute("href")
	// fmt.Println("%v", hRefLink)

	book.ImageLink, _ = post.Locator(".postContent img").GetAttribute("src")

	// fmt.Println("%v \n", imgSrc)

	book.HrefLink = "https://audiobookbay.lu" + book.HrefLink

	magnetLink := getMagnetLink(page, book.HrefLink)
	book.MagnetLink = magnetLink
	book.Source = "Audiobookbay"
	// fmt.Printf("Fetch Book: %s", book)
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
