package urlutil

import "testing"

func TestIsVideoPlatformURL(t *testing.T) {
	tests := []struct {
		url  string
		want bool
	}{
		{"", false},
		{"https://www.youtube.com/watch?v=abc", true},
		{"https://youtube.com/watch?v=abc", true},
		{"https://youtu.be/abc123", true},
		{"https://www.instagram.com/p/ABC/", true},
		{"https://instagram.com/reel/xyz", true},
		{"https://example.com/file.pdf", false},
		{"https://google.com", false},
		{"not-a-url", false},
		{"https://sub.instagram.com/x", false},
	}
	for _, tt := range tests {
		got := IsVideoPlatformURL(tt.url)
		if got != tt.want {
			t.Errorf("IsVideoPlatformURL(%q) = %v, want %v", tt.url, got, tt.want)
		}
	}
}
