package zlib

import (
	"net/http"
	"os"

	"net/url"

	"fmt"

	"strings"

	"io"

	"encoding/json"
)

type Client struct {
	BaseURL    string
	UserID     int
	UserKey    string
	Downloads  int
	HttpClient http.Client
}

type loginResponse struct {
	Success int    `json:"success"`
	Error   string `json:"error"`
	User    user   `json:"user"`
}

type searchResponse struct {
	Success int     `json:"success"`
	Error   string  `json:"error"`
	Books   []EBook `json:"books"`
}

type downloadResponse struct {
	Success int         `json:"success"`
	Error   interface{} `json:"error"`
	File    file        `json:"file"`
}
type user struct {
	ID        int    `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	UserKey   string `json:"remix_userkey"`
	Downloads int    `json:"downloads_today"`
}

type file struct {
	Link      string `json:"downloadLink"`
	Extension string `json:"extension"`
}
type EBook struct {
	Title     string `json:"title"`
	Author    string `json:"author"`
	Year      int    `json:"year"`
	ImageLink string `json:"cover"`
	ID        int    `json:"id"`
	Extension string `json:"extension"`
	Hash      string `json:"hash"`
}

func NewClient() *Client {
	return &Client{
		BaseURL:    "https://z-library.sk",
		HttpClient: http.Client{},
	}
}
func (c *Client) login(email string, password string) error {
	endpoint := fmt.Sprintf("%s/eapi/user/login", c.BaseURL)

	form := url.Values{}
	form.Set("email", email)
	form.Set("password", password)

	// fmt.Println("Test")
	req, err := http.NewRequest("POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	// Set Content-Type for form data
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// fmt.Println("Test2")
	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var response loginResponse

	// fmt.Println("Test3")
	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}

	if response.Success != 1 {
		return fmt.Errorf("%v", response.Error)
	}

	// fmt.Printf("%v", response)
	c.UserID = response.User.ID
	c.UserKey = response.User.UserKey
	c.Downloads = response.User.Downloads

	return nil
}

func (c *Client) searchBook(title string) (*EBook, error) {
	endpoint := fmt.Sprintf("%s/eapi/book/search", c.BaseURL)

	form := url.Values{}
	form.Set("message", title)

	req, err := http.NewRequest("POST", endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}

	// Set Content-Type for form data
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	req.Header.Set("remix-userid", fmt.Sprintf("%v", c.UserID))
	req.Header.Set("remix-userkey", c.UserKey)

	req.AddCookie(&http.Cookie{Name: "remix_userid", Value: fmt.Sprintf("%v", c.UserID)})
	req.AddCookie(&http.Cookie{Name: "remix_userkey", Value: c.UserKey})

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("zlib returned %d  %s", resp.StatusCode, body)
	}
	// body, _ := io.ReadAll(resp.Body)

	var response searchResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	if response.Success != 1 {
		return nil, fmt.Errorf("%v", response.Error)
	}

	// fmt.Printf("%v", response)
	return &response.Books[0], nil
}

func (c *Client) downloadBook(book EBook, outputDir string, userID string, userKey string) error {
	endpoint := fmt.Sprintf("%s/eapi/book/%v/%v/file", c.BaseURL, book.ID, book.Hash)

	// fmt.Printf("Endpoint: %v\n", endpoint)
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return err
	}

	// Set Content-Type for form data
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	req.Header.Set("remix-userid", fmt.Sprintf("%v", userID))
	req.Header.Set("remix-userkey", userKey)

	req.AddCookie(&http.Cookie{Name: "remix_userid", Value: fmt.Sprintf("%v", c.UserID)})
	req.AddCookie(&http.Cookie{Name: "remix_userkey", Value: c.UserKey})

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return err
	}

	body, _ := io.ReadAll(resp.Body)

	var response downloadResponse

	if err := json.Unmarshal(body, &response); err != nil {
		return err
	}

	if response.Success != 1 {
		// fmt.Print("Error here")
		return fmt.Errorf("%v", response.Error)
	}

	bookTitle := strings.ReplaceAll(book.Title, " ", "_")
	fileName := fmt.Sprintf("%s.%s", bookTitle, response.File.Extension)
	outputPath := fmt.Sprintf("%s/%s", strings.TrimRight(outputDir, "/"), fileName)

	err = downloadFile(outputPath, response.File.Link)

	// fmt.Printf("Response: %v\n", response)

	if err != nil {
		return err
	}

	return nil
}

func downloadFile(filepath string, url string) error {
	// Get the data

	fmt.Println(filepath)
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}
