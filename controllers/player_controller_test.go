package controllers

import "testing"

func TestIsValidMojangUsername(t *testing.T) {
	tests := []struct {
		name     string
		username string
		want     bool
	}{
		{name: "valid letters and digits", username: "Player123", want: true},
		{name: "valid underscore", username: "Player_Name", want: true},
		{name: "empty", username: "", want: false},
		{name: "too long", username: "abcdefghijklmnopq", want: false},
		{name: "invalid punctuation", username: "Player.Name", want: false},
		{name: "non ascii", username: "玩家", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isValidMojangUsername(test.username); got != test.want {
				t.Fatalf("isValidMojangUsername(%q) = %t, want %t", test.username, got, test.want)
			}
		})
	}
}
