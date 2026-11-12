package main

import (
	"strconv"
	"strings"
)

const (
	folderPrefix  = "NET_"
	maxFolderName = 32 // the firmware allows 64; shorter leaves room for file names
	maxDevicePath = 95 // the whole path, leaving one byte of the 96 for the terminator
)

// slug keeps only ASCII letters and digits, replacing runs of anything else with "_".
func slug(s string) string {
	var b strings.Builder
	prevSep := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevSep = false
		case !prevSep && b.Len() > 0:
			b.WriteByte('_')
			prevSep = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// folderNameFor turns a display name into a safe SD folder name, e.g. "Gym Mix" -> "NET_Gym_Mix".
func folderNameFor(display string) string {
	s := slug(display)
	if s == "" {
		s = "Playlist"
	}
	name := folderPrefix + s
	if len(name) > maxFolderName {
		name = name[:maxFolderName]
	}
	return strings.TrimRight(name, "_")
}

// deviceName builds the file name for the SD card, e.g. "17-Daft_Punk_One_More_Time.mp3".
func deviceName(id int64, artist, title, folder string) string {
	budget := maxDevicePath - len(folder) - len("//") - len(".mp3")
	name := strconv.FormatInt(id, 10) + "-" + slug(artist+" "+title)
	name = strings.TrimRight(name, "-_")
	if len(name) > budget {
		name = name[:budget]
	}
	return name + ".mp3"
}
