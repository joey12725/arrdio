package zlib

import (
	"fmt"
	"strings"

	db "github.com/ashkaty/arrdio-api/services"
)

func queryZLibUser(query []string) ([]ZLibUser, error) {
	var zLibUsers []ZLibUser

	if len(query) == 0 {
		return nil, fmt.Errorf("no columns provided for query")
	}

	// Join columns into a SQL-safe string
	selectClause := strings.Join(query, ", ")

	selectQueryString := fmt.Sprintf("SELECT %v FROM zlibuser", selectClause)

	selectQuery, err := db.DB.Query(selectQueryString)

	if err != nil {

		return nil, err
	}

	for selectQuery.Next() {

		zLibUser := ZLibUser{}

		extractedColumns := make([]any, len(query))
		for i, column := range query {
			// fmt.Println(column)
			switch column {
			case "id":
				extractedColumns[i] = &zLibUser.ID
			case "email":
				extractedColumns[i] = &zLibUser.Email
			case "download_count":
				extractedColumns[i] = &zLibUser.Download_Count
			case "password":
				extractedColumns[i] = &zLibUser.Password
			case "user_id":
				extractedColumns[i] = &zLibUser.User_ID
			case "user_key":
				extractedColumns[i] = &zLibUser.User_Key
			default:
				return nil, fmt.Errorf("unsupported column: %s", column)
			}
		}

		err = selectQuery.Scan(extractedColumns...)
		if err != nil {
			return nil, err
		}

		zLibUsers = append(zLibUsers, zLibUser)

	}

	// fmt.Println(zLibUsers)
	return zLibUsers, nil
}

func getSavePath(category string) string {
	selectQuery := db.DB.QueryRow("SELECT savepath FROM categories WHERE category=?", category)

	var savePath string
	err := selectQuery.Scan(&savePath)

	if err != nil {
		defaultQuery := db.DB.QueryRow("SELECT savepath FROM categories WHERE category=default", category)

		defaultQuery.Scan(&savePath)
	}

	return savePath
}

// TODO: Finish refresh function and set it up as cron job

func RefreshZLibUser() error {
	zLibClient := NewClient()

	zLibUsers, err := queryZLibUser([]string{"email", "password"})

	if err != nil {
		return fmt.Errorf("error refreshing zlibrary user: %v", err.Error())
	}

	var loginError error
	var insertError error
	for _, zLibUser := range zLibUsers {
		loginError = zLibClient.login(zLibUser.Email, zLibUser.Password)
		// fmt.Printf("User ID: %v, User Key: %v\n", zLibClient.UserID, zLibClient.UserKey)
		updateStatement, _ := db.DB.Prepare("UPDATE zlibuser SET user_id = ?, user_key = ?, download_count = ? WHERE email = ?")
		_, insertError = updateStatement.Exec(zLibClient.UserID, zLibClient.UserKey, zLibClient.Downloads, zLibUser.Email)
	}
	if loginError != nil {
		return fmt.Errorf("error logging into zlibrary: %v", loginError)
	}

	if insertError != nil {
		return fmt.Errorf("error inserting zlibrary user: %v", insertError)
	}
	return nil
}
