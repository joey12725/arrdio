package db

import (
	"database/sql"

	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func Init() error {

	var err error
	DB, err = sql.Open("sqlite3", "arrdio.db")

	if err != nil {
		return err
	}

	categoriesTable := `
		CREATE TABLE IF NOT EXISTS categories (
  		id INTEGER PRIMARY KEY,	
  		category TEXT UNIQUE NOT NULL,
  		savepath TEXT NOT NULL
		);`

	_, err = DB.Exec(categoriesTable)

	if err != nil {
		return err
	}

	zLibUserTable := `
		CREATE TABLE IF NOT EXISTS zlibuser (
		id INTEGER PRIMARY KEY,
		email TEXT UNIQUE NOT NULL,
		password TEXT NOT NULL,
		download_count INTEGER NOT NULL,
		user_id TEXT UNIQUE,
		user_key TEXT UNIQUE
		)
	`

	_, err = DB.Exec(zLibUserTable)

	if err != nil {
		return err
	}

	downloadClientTable := `
		CREATE TABLE IF NOT EXISTS downloadclient (
		id INTEGER PRIMARY KEY,
		url TEXT UNIQUE NOT NULL,
		username TEXT NOT NULL,
		password INTEGER NOT NULL	
		)
	`

	_, err = DB.Exec(downloadClientTable)

	if err != nil {
		return err
	}

	initalizeSavePath := `INSERT OR IGNORE INTO categories (category, savepath) VALUES ('audiobooks', '/audiobooks'), ('ebooks', '/ebooks')`

	_, err = DB.Exec(initalizeSavePath)

	if err != nil {
		return err
	}

	return nil
}
