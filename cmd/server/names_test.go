package main

import (
	"strings"
	"testing"
)

func TestDeviceName(t *testing.T) {
	cases := []struct {
		id            int64
		artist, title string
		folder        string
		want          string
	}{
		{17, "Daft Punk", "One More Time", "NET_Gym", "17-Daft_Punk_One_More_Time.mp3"},
		{18, "", "आज जाने की ज़िद न करो", "NET_Gym", "18.mp3"},
		{19, "AC/DC", "Back In Black?", "NET_Gym", "19-AC_DC_Back_In_Black.mp3"},
	}
	for _, c := range cases {
		got := deviceName(c.id, c.artist, c.title, c.folder)
		if got != c.want {
			t.Errorf("deviceName(%d, %q, %q) = %q, want %q", c.id, c.artist, c.title, got, c.want)
		}
	}
}

func TestDeviceNameFitsPath(t *testing.T) {
	long := strings.Repeat("a", 200)
	got := deviceName(5, long, long, "NET_Gym")
	path := "/NET_Gym/" + got
	if len(path) > maxDevicePath {
		t.Errorf("path is %d characters, limit is %d", len(path), maxDevicePath)
	}
}

func TestFolderNameFor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"Gym Mix", "NET_Gym_Mix"},
		{"", "NET_Playlist"},
		{"जिम", "NET_Playlist"},
	}
	for _, c := range cases {
		if got := folderNameFor(c.in); got != c.want {
			t.Errorf("folderNameFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := folderNameFor(strings.Repeat("x", 100))
	if len(long) > maxFolderName {
		t.Errorf("folder name %q is %d characters, limit is %d", long, len(long), maxFolderName)
	}
}
