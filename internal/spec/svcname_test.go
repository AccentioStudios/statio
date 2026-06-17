package spec

import "testing"

func TestValidServiceName(t *testing.T) {
	valid := []string{"api", "bohemian-gym-api", "a", "web2", "x-1-2"}
	for _, s := range valid {
		if !ValidServiceName(s) {
			t.Errorf("ValidServiceName(%q) = false, want true", s)
		}
	}
	invalid := []string{
		"",                 // empty
		"bohemian_gym_api", // underscores (the real-world bug)
		"1api",             // starts with a digit
		"-api",             // starts with a dash
		"API",              // uppercase
		"this-name-is-way-too-long-to-be-valid-x", // > 31 chars
	}
	for _, s := range invalid {
		if ValidServiceName(s) {
			t.Errorf("ValidServiceName(%q) = true, want false", s)
		}
	}
}
