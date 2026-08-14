package zlib

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ashkaty/arrdio-api/services/models"
)

type Quota = int

// SearchBook handles ebook searches on ZLibrary
// @Summary Search ebooks on ZLibrary
// @Tags ZLibrary, Deprecieate
// @Accept json
// @Produce json
// @Param request body models.Request true "Search request"
// @Success 200 {object} models.EbookResult
// @Failure 400 {string} string "Invalid request"
// @Failure 424 {string} string "No Zlibrary Users Defined"
// @Failure 500 {string} string "Server error"
// @Router /zlib/searchbook [post]
// @Deprecated
func SearchBook(w http.ResponseWriter, r *http.Request) {
	var request models.Request

	err := json.NewDecoder(r.Body).Decode(&request)

	if err != nil {
		http.Error(w, "error with request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if len(request.Request) == 0 {
		http.Error(w, "error request body must not be empty", http.StatusBadRequest)
		return
	}

	zLibClient := NewClient()

	zLibUsers, err := queryZLibUser([]string{"email", "password"})
	if len(zLibUsers) == 0 {
		http.Error(w, "error no zlibrary users defined", http.StatusFailedDependency)
	}

	zLibClient.login(zLibUsers[0].Email, zLibUsers[0].Password)
	var responseList []models.EbookResultItem

	for _, bookSearch := range request.Request {

		// fmt.Printf("This is book title and author %v , %v\n", bookSearch.BookItem.Title, bookSearch.BookItem.Author)
		book, err := zLibClient.searchBook(fmt.Sprintf("%v", bookSearch.Book.Title))

		responseList = append(responseList, models.EbookResultItem{Title: bookSearch.Book.Title, EBook: book, Error: err.Error()})
	}

	// fmt.Printf("%v\n", responseList)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(responseList)

}

// DownloadBook handles ebook downloads from ZLibrary
// @Summary Download ebooks from ZLibrary
// @Tags ZLibrary
// @Accept json
// @Produce json
// @Param request body DownloadRequest true "Download request"
// @Success 200 {object} DownloadResponse
// @Success 206 {object} DownloadResponse "Partial content (quota exceeded)"
// @Failure 400 {string} string "Invalid request"
// @Failure 500 {string} string "Server error"
// @Router /zlib/downloadbook [post]
// @Deprecated
func DownloadBook(w http.ResponseWriter, r *http.Request) {
	var request DownloadRequest
	var response DownloadResponse

	// fmt.Println("Hi")
	err := json.NewDecoder(r.Body).Decode(&request)

	if err != nil {
		http.Error(w, "error with request body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// fmt.Println("Test")
	if len(request.Request) == 0 {
		http.Error(w, "error request body must not be empty", http.StatusBadRequest)
		return
	}

	warning := ""

	refreshError := RefreshZLibUser()

	if refreshError != nil {
		warning = fmt.Sprintf("%v\n %v", warning, refreshError.Error())
	}

	zLibUsers, err := queryZLibUser([]string{"id", "email", "download_count", "password", "user_id", "user_key"})

	remainingQuota := make([]Quota, len(zLibUsers))

	if err != nil {
		fmt.Printf("error getting zlibrary users: %v", err.Error())
		http.Error(w, "error getting zlibrary users: "+err.Error(), http.StatusInternalServerError)
		return
	}

	fmt.Printf("Request: %v\n", request)
	var totalDownloadCount int

	for i, user := range zLibUsers {
		remaining := 10 - user.Download_Count
		if remaining < 0 {
			remaining = 0
		}
		totalDownloadCount += remaining
		remainingQuota[i] = remaining
	}

	// fmt.Println("Test4")

	downloadList := request.Request
	if totalDownloadCount < len(request.Request) {
		warning = fmt.Sprintf("quota only allows downloading %d of %d books; remaining items were skipped", totalDownloadCount, len(request.Request))
		downloadList = downloadList[:totalDownloadCount]
	}

	zLibIndex := 0

	for _, download := range downloadList {
		found := false
		for index, quota := range remainingQuota {
			if quota > 0 {
				zLibIndex = index
				found = true
				break
			}
		}

		if !found {
			break
		}

		zLibClient := NewClient()

		zLibClient.UserID = int(zLibUsers[zLibIndex].ID)
		zLibClient.UserKey = zLibUsers[zLibIndex].User_Key

		savePath := getSavePath(download.Category)
		// fmt.Println(savePath)
		downloadErr := zLibClient.downloadBook(download.BookItem, savePath, zLibUsers[zLibIndex].User_ID, zLibUsers[zLibIndex].User_Key)

		if downloadErr != nil {
			fmt.Printf("error calling zlibrary's endpoint: %v\n", downloadErr.Error())
			errorMessage := fmt.Sprintf("error calling zlibrary's endpoint: %v", downloadErr.Error())
			response.Response = append(response.Response, DownloadResult{BookItem: download.BookItem, Error: errorMessage})
			continue
		}

		remainingQuota[zLibIndex]--
		response.Response = append(response.Response, DownloadResult{BookItem: download.BookItem, Error: ""})

	}

	if warning != "" {
		response.Warning = warning
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusPartialContent)
		json.NewEncoder(w).Encode(response)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(response)
}
