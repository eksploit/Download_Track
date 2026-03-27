package logutil

import "unicode/utf8"

// TruncateString обрезает строку до max рун (для URL и путей в логах).
// При обрезке в конец добавляется «...». Если max < 4, возвращается «...».
func TruncateString(s string, max int) string {
	if max < 4 {
		return "..."
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	runes := []rune(s)
	if len(runes) > max-3 {
		runes = runes[:max-3]
	}
	return string(runes) + "..."
}
