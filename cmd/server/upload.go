package main

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/dhowden/tag"
)

const (
	maxUpload = 50 << 20 // 50 MB
	songsDir  = "data/songs"
	tmpDir    = "data/tmp"
)

// looksLikeMP3 is a sanity check, not a security boundary: an ID3 tag
// at the start, or an MPEG audio frame sync.
func looksLikeMP3(head []byte) bool {
	if len(head) < 3 {
		return false
	}
	if string(head[:3]) == "ID3" {
		return true
	}
	return head[0] == 0xFF && head[1]&0xE0 == 0xE0
}

func serverError(w http.ResponseWriter, what string, err error) {
	log.Println(what+":", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "use POST", http.StatusMethodNotAllowed)
		return
	}

	// Refuse to read more than maxUpload bytes in total.
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			http.Error(w, "file too large (max 50 MB)", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "bad upload", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing 'file' field", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// 1. Does it look like an MP3?
	head := make([]byte, 3)
	if _, err := io.ReadFull(file, head); err != nil || !looksLikeMP3(head) {
		http.Error(w, "not an MP3 file", http.StatusUnsupportedMediaType)
		return
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		serverError(w, "seek", err)
		return
	}

	// 2. Copy to a temp file and hash it in the same pass.
	tmp, err := os.CreateTemp(tmpDir, "upload-*.part")
	if err != nil {
		serverError(w, "create temp", err)
		return
	}
	defer os.Remove(tmp.Name()) // harmless after a successful rename

	h := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, h), file)
	if err != nil {
		tmp.Close()
		serverError(w, "copy", err)
		return
	}
	if err := tmp.Close(); err != nil { // must be closed before renaming on Windows
		serverError(w, "close temp", err)
		return
	}
	sum := hex.EncodeToString(h.Sum(nil))

	// 3. Duplicate?
	var existing int64
	err = db.QueryRow(`SELECT id FROM songs WHERE sha256 = ?`, sum).Scan(&existing)
	if err == nil {
		http.Error(w, fmt.Sprintf("already in the archive (song %d)", existing), http.StatusConflict)
		return
	}
	if !errors.Is(err, sql.ErrNoRows) {
		serverError(w, "duplicate check", err)
		return
	}

	// 4. Read tags; fall back to the file name for the title.
	title, artist, album := "", "", ""
	if _, err := file.Seek(0, io.SeekStart); err == nil {
		if md, err := tag.ReadFrom(file); err == nil {
			title, artist, album = md.Title(), md.Artist(), md.Album()
		}
	}
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSuffix(header.Filename, filepath.Ext(header.Filename))
	}

	// 5. Insert the row, then move the file to its final name.
	res, err := db.Exec(`INSERT INTO songs (title, artist, album, size_bytes, sha256)
	                     VALUES (?, ?, ?, ?, ?)`, title, artist, album, size, sum)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			http.Error(w, "already in the archive", http.StatusConflict)
			return
		}
		serverError(w, "insert", err)
		return
	}
	id, err := res.LastInsertId()
	if err != nil {
		serverError(w, "last insert id", err)
		return
	}
	final := filepath.Join(songsDir, fmt.Sprintf("%d.mp3", id))
	if err := os.Rename(tmp.Name(), final); err != nil {
		db.Exec(`DELETE FROM songs WHERE id = ?`, id) // undo the row
		serverError(w, "rename", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(Song{
		ID: id, Title: title, Artist: artist, Album: album,
		SizeBytes: size, SHA256: sum,
	})
}
