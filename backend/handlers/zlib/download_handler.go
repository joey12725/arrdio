package zlib

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Quota = int

func SearchBook(w http.ResponseWriter, r *http.Request) {
	var request SearchRequest

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

	var responseList SearchResponse

	// fmt.Printf("%v", request.Request)
	for _, bookSearch := range request.Request {

		// fmt.Printf("This is book title and author %v , %v\n", bookSearch.BookItem.Title, bookSearch.BookItem.Author)
		books, err := zLibClient.searchBook(fmt.Sprintf("%v , %v", bookSearch.BookItem.Title, bookSearch.BookItem.Title))

		responseList.Response = append(responseList.Response, SearchResult{Books: books, Error: err, Title: bookSearch.BookItem.Title})
	}

	// fmt.Printf("%v\n", responseList)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(responseList)

}

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
