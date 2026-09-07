package main

import "testing"

func TestResetAvatarClearsPhotoAndPreservesRoomSettings(t *testing.T) {
	t.Setenv("GROKBOX_HOME", t.TempDir())
	m := NewManager()
	m.prefs = Prefs{Rooms: map[string]RoomPrefs{
		"room": {Photo: "uploaded.png", Shape: "hex", Color: "red", Nickname: "Team", Quiet: true},
	}}
	a := &API{m: m}
	if err := a.SetAvatar("room", "", ""); err != nil {
		t.Fatal(err)
	}
	want := RoomPrefs{Nickname: "Team", Quiet: true}
	if got := loadPrefs().Rooms["room"]; got != want {
		t.Fatalf("saved reset = %+v, want %+v", got, want)
	}
}
