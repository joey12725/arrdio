package zlib

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	db "github.com/ashkaty/arrdio-api/services"

	"encoding/json"

	"github.com/mattn/go-sqlite3"

	"github.com/go-chi/chi/v5"
)

func PutCategory(w http.ResponseWriter, r *http.Request) {

	var request CategoryRequest

	err := json.NewDecoder(r.Body).Decode(&request)
	if err != nil {
		http.Error(w, "error with request body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if request.Category == "" || request.SavePath == "" {
		http.Error(w, "category and request must be not empty", http.StatusBadRequest)
		return
	}

	insertString := fmt.Sprintf(`INSERT INTO categories (category, savepath) VALUES ('%v', '%v')`, request.Category, request.SavePath)
	_, err = db.DB.Exec(insertString)

	if err != nil {
		var sqliteErr sqlite3.Error
		if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
			http.Error(w, "error inserting into database, duplicate entry", http.StatusBadRequest)
			return
		}
		http.Error(w, "error inserting into database: "+err.Error(), http.StatusInternalServerError)
	}

	row := db.DB.QueryRow(`
    SELECT id, category, savepath
    FROM categories 
    WHERE category=?`, request.Category)

	var categoryResponse CategoryResponse

	if err = row.Scan(&categoryResponse.ID, &categoryResponse.Category, &categoryResponse.SavePath); err == sql.ErrNoRows {
		http.Error(w, "error unable to validate category insertion: "+err.Error(), http.StatusInternalServerError)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(categoryResponse)

}

func GetCateory(w http.ResponseWriter, r *http.Request) {

	var categoriesList []CategoryResponse
	selectQuery, err := db.DB.Prepare("SELECT * FROM categories")

	if err != nil {
		http.Error(w, "error preparing query: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := selectQuery.Query()

	if err != nil {
		http.Error(w, "error with query: "+err.Error(), http.StatusInternalServerError)
		return
	}

	for rows.Next() {
		var category CategoryResponse

		err := rows.Scan(&category.ID, &category.Category, &category.SavePath)

		if err != nil {
			http.Error(w, "error scanning categories: "+err.Error(), http.StatusInternalServerError)
			return
		}

		categoriesList = append(categoriesList, category)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(categoriesList)

}
func PutZLibUser(w http.ResponseWriter, r *http.Request) {

	var request ZLibUserRequest

	err := json.NewDecoder(r.Body).Decode(&request)

	if err != nil {
		http.Error(w, "error with response header: "+err.Error(), http.StatusBadRequest)
		return
	}

	if request.Email == "" || request.Password == "" {
		http.Error(w, "email and password must be not empty", http.StatusBadRequest)
		return
	}

	zLibClient := NewClient()

	err = zLibClient.login(request.Email, request.Password)

	if err != nil {
		http.Error(w, "error, unable to login with provided credentials: %v"+err.Error(), http.StatusBadRequest)
		return
	}

	// insertString := fmt.Sprintf(``, request.Email, request.Password, zLibClient.Downloads, zLibClient.UserID, zLibClient.UserKey)

	insertStatement, _ := db.DB.Prepare("INSERT INTO zlibuser (email, password, download_count, user_id, user_key) VALUES (?, ?, ?, ?, ?)")
	_, err = insertStatement.Exec(request.Email, request.Password, zLibClient.Downloads, zLibClient.UserID, zLibClient.UserKey)

	if err != nil {
		var sqliteErr sqlite3.Error
		if errors.As(err, &sqliteErr) && sqliteErr.ExtendedCode == sqlite3.ErrConstraintUnique {
			http.Error(w, "error inserting into database, duplicate entry", http.StatusBadRequest)
			return
		}
		http.Error(w, "error inserting into database: "+err.Error(), http.StatusInternalServerError)
		return
	}

	row := db.DB.QueryRow(`SELECT id, email, download_count FROM zlibuser WHERE email=?`, request.Email)

	var zLibUserResponse ZLibUser

	err = row.Scan(&zLibUserResponse.ID, &zLibUserResponse.Email, &zLibUserResponse.Download_Count)

	if err != nil {
		http.Error(w, "error unable to validate zlibuser insertion: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(zLibUserResponse)
}

// TODO: Finish refresh function and set it up as cron job

func GetZLibUser(w http.ResponseWriter, r *http.Request) {

	zLibUsers, err := queryZLibUser([]string{"id", "email", "download_count"})

	if err != nil {
		http.Error(w, "error getting zlibrary users: "+err.Error(), http.StatusInternalServerError)
	}

	// fmt.Printf("%v", zLibUsers)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(zLibUsers)
}

func DeleteUser(w http.ResponseWriter, r *http.Request) {
	email := chi.URLParam(r, "email")
	// fmt.Print(email)
	deleteStatement, _ := db.DB.Prepare("DELETE FROM zlibuser WHERE email=?")

	response, err := deleteStatement.Exec(email)

	if err != nil {
		http.Error(w, "error deleting zlibrary user: "+err.Error(), http.StatusInternalServerError)
	}

	rows, _ := response.RowsAffected()
	if rows == 0 {
		http.Error(w, "no user deleted, check if emails exist", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Delete successful\n"))
}

func DeleteCategory(w http.ResponseWriter, r *http.Request) {
	category := chi.URLParam(r, "category")
	// fmt.Print(category)
	deleteStatement, _ := db.DB.Prepare("DELETE FROM categories WHERE category=?")

	response, err := deleteStatement.Exec(category)

	if err != nil {
		http.Error(w, "error deleting category user: "+err.Error(), http.StatusInternalServerError)
	}

	rows, _ := response.RowsAffected()
	if rows == 0 {
		http.Error(w, "no category deleted, check if category exist", http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Delete successful\n"))
}
