package delivery

import "strings"

// isURL возвращает true, если s выглядит как HTTP(S) URL (иначе считается путём к файлу).
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}
