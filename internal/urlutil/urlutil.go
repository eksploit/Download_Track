package urlutil

import (
	"net/url"
	"strings"
)

// Домены видео-платформ, для которых используется yt-dlp (YouTube, Instagram).
var videoPlatformHosts = map[string]bool{
	"youtube.com":       true,
	"www.youtube.com":   true,
	"youtu.be":          true,
	"instagram.com":     true,
	"www.instagram.com": true,
}

// IsVideoPlatformURL возвращает true, если URL относится к поддерживаемой видео-платформе
// (YouTube, Instagram). Для таких ссылок бот сразу отправляет видео в чат через yt-dlp.
func IsVideoPlatformURL(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	if videoPlatformHosts[host] {
		return true
	}
	// без префикса www.
	hostNoWWW := strings.TrimPrefix(host, "www.")
	return videoPlatformHosts[hostNoWWW]
}
