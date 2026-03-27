package logutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateString(t *testing.T) {
	if got := TruncateString("abc", 10); got != "abc" {
		t.Errorf("короткая строка: got %q", got)
	}
	long := strings.Repeat("a", 300)
	got := TruncateString(long, 256)
	if utf8.RuneCountInString(got) != 256 {
		t.Errorf("ожидалось 256 рун с учётом суффикса, got %d", utf8.RuneCountInString(got))
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("ожидался суффикс ...: %q", got)
	}
}
