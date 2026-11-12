package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Playlist struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	FolderName string `json:"folder_name"`
	SongCount  int    `json:"song_count"`
}

// createPlaylist inserts a playlist, adding _2, _3, ... if the folder name is taken.
func createPlaylist(name string) (Playlist, error) {
	base := folderNameFor(name)
	candidate := base
	for i := 2; i < 100; i++ {
		res, err := db.Exec(`INSERT INTO playlists (name, folder_name) VALUES (?, ?)`, name, candidate)
		if err == nil {
			id, _ := res.LastInsertId()
			return Playlist{ID: id, Name: name, FolderName: candidate}, nil
		}
		if !strings.Contains(err.Error(), "UNIQUE") {
			return Playlist{}, err
		}
		suffix := "_" + strconv.Itoa(i)
		cut := base
		if len(cut)+len(suffix) > maxFolderName {
			cut = cut[:maxFolderName-len(suffix)]
		}
		candidate = strings.TrimRight(cut, "_") + suffix
	}
	return Playlist{}, errors.New("could not find a free folder name")
}

func createPlaylistHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || utf8.RuneCountInString(req.Name) > 50 {
		http.Error(w, "name must be 1-50 characters", http.StatusBadRequest)
		return
	}
	p, err := createPlaylist(req.Name)
	if err != nil {
		serverError(w, "create playlist", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(p)
}

func listPlaylistsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query(`
		SELECT p.id, p.name, p.folder_name, COUNT(s.id)
		FROM playlists p LEFT JOIN songs s ON s.playlist_id = p.id
		GROUP BY p.id ORDER BY p.id`)
	if err != nil {
		serverError(w, "list playlists", err)
		return
	}
	defer rows.Close()

	out := []Playlist{}
	for rows.Next() {
		var p Playlist
		if err := rows.Scan(&p.ID, &p.Name, &p.FolderName, &p.SongCount); err != nil {
			serverError(w, "scan playlist", err)
			return
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		serverError(w, "playlist rows", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(out)
}

// PUT /api/songs/{id}/playlist with {"playlist_id": 3} or {"playlist_id": null}
func setSongPlaylistHandler(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad song id", http.StatusBadRequest)
		return
	}
	var req struct {
		PlaylistID *int64 `json:"playlist_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if req.PlaylistID != nil {
		var one int
		err := db.QueryRow(`SELECT 1 FROM playlists WHERE id = ?`, *req.PlaylistID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "no such playlist", http.StatusNotFound)
			return
		} else if err != nil {
			serverError(w, "playlist check", err)
			return
		}
	}
	res, err := db.Exec(`UPDATE songs SET playlist_id = ? WHERE id = ?`, req.PlaylistID, id)
	if err != nil {
		serverError(w, "update song", err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		http.Error(w, "no such song", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
