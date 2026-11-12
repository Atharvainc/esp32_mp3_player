package main

import (
	"database/sql"
	"encoding/json"
	"log"
	"net/http"

	_ "modernc.org/sqlite"
)

var db *sql.DB

const schema = `
CREATE TABLE IF NOT EXISTS playlists (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT NOT NULL,
	folder_name TEXT NOT NULL UNIQUE COLLATE NOCASE,
	created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS songs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	title       TEXT NOT NULL,
	artist      TEXT NOT NULL DEFAULT '',
	album       TEXT NOT NULL DEFAULT '',
	duration_s  INTEGER NOT NULL DEFAULT 0,
	size_bytes  INTEGER NOT NULL,
	sha256      TEXT NOT NULL UNIQUE,
	playlist_id INTEGER REFERENCES playlists(id) ON DELETE SET NULL,
	created_at  TEXT NOT NULL DEFAULT CURRENT_TIMESTAMP
);`

func openDB(path string) error {
	var err error
	db, err = sql.Open("sqlite",
		"file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	_, err = db.Exec(schema)
	return err
}

type Song struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	DurationS  int    `json:"duration_s"`
	SizeBytes  int64  `json:"size_bytes"`
	SHA256     string `json:"sha256"`
	PlaylistID *int64 `json:"playlist_id"`
}

func listSongs() ([]Song, error) {
	rows, err := db.Query(`SELECT id, title, artist, album, duration_s,
	                              size_bytes, sha256, playlist_id
	                       FROM songs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	songs := []Song{}
	for rows.Next() {
		var s Song
		if err := rows.Scan(&s.ID, &s.Title, &s.Artist, &s.Album,
			&s.DurationS, &s.SizeBytes, &s.SHA256, &s.PlaylistID); err != nil {
			return nil, err
		}
		songs = append(songs, s)
	}
	return songs, rows.Err()
}

func songsHandler(w http.ResponseWriter, r *http.Request) {
	songs, err := listSongs()
	if err != nil {
		log.Println("list songs:", err)
		http.Error(w, "database error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(songs)
}
