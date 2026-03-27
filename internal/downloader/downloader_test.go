package downloader

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaysLeftCeil_ExpiredOrZero(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	exp := now
	if got := DaysLeftCeil(now, exp); got != 0 {
		t.Errorf("в момент истечения ожидалось 0, получено %d", got)
	}
	if got := DaysLeftCeil(now, now.Add(-time.Hour)); got != 0 {
		t.Errorf("после истечения ожидалось 0, получено %d", got)
	}
}

func TestDaysLeftCeil_CeilBoundaries(t *testing.T) {
	now := time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)
	// Чуть больше 24 ч → 2 «дня» по ceil
	exp := now.Add(25*time.Hour + time.Minute)
	if got := DaysLeftCeil(now, exp); got != 2 {
		t.Errorf("25ч+ ожидалось 2, получено %d", got)
	}
	// Чуть меньше 48 ч → всё ещё 2
	exp2 := now.Add(47 * time.Hour)
	if got := DaysLeftCeil(now, exp2); got != 2 {
		t.Errorf("47ч ожидалось 2, получено %d", got)
	}
	// Ровно 24 ч → 1
	exp3 := now.Add(24 * time.Hour)
	if got := DaysLeftCeil(now, exp3); got != 1 {
		t.Errorf("24ч ожидалось 1, получено %d", got)
	}
}

func TestCookieExpiry_NotFound(t *testing.T) {
	_, err := CookieExpiry("/nonexistent/cookies.txt")
	if err == nil {
		t.Fatal("ожидалась ошибка для несуществующего файла")
	}
	// Ошибка обёрнута в "чтение файла cookies: ..."
	if err.Error() == "" {
		t.Fatalf("ожидалось непустое сообщение об ошибке, получено: %v", err)
	}
}

func TestCookieExpiry_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := CookieExpiry(path)
	if err == nil {
		t.Fatal("ожидалась ошибка для пустого файла")
	}
}

func TestCookieExpiry_WhitespaceOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blank.txt")
	if err := os.WriteFile(path, []byte("  \n\t  \n"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := CookieExpiry(path)
	if err == nil {
		t.Fatal("ожидалась ошибка для файла только с пробелами")
	}
}

func TestCookieExpiry_Netscape(t *testing.T) {
	// Минимальная дата истечения в строке — 2000000000 (Unix) = 2033-05-18
	// Вторая строка — 1000000000 = 2001-09-09 — должна быть минимумом.
	dir := t.TempDir()
	path := filepath.Join(dir, "netscape.txt")
	content := "# Netscape HTTP Cookie File\n" +
		".instagram.com\tTRUE\t/\tTRUE\t2000000000\tname1\tvalue1\n" +
		".instagram.com\tTRUE\t/\tTRUE\t1000000000\tname2\tvalue2\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	expiry, err := CookieExpiry(path)
	if err != nil {
		t.Fatalf("CookieExpiry: %v", err)
	}
	expected := time.Unix(1000000000, 0)
	if !expiry.Equal(expected) {
		t.Errorf("ожидалась дата %v, получено %v", expected, expiry)
	}
}

func TestCookieExpiry_Netscape_OnlyComments(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "comments.txt")
	content := "# Netscape HTTP Cookie File\n# comment\n"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := CookieExpiry(path)
	if err == nil {
		t.Fatal("ожидалась ошибка при отсутствии записей с датой истечения")
	}
}

func TestCookieExpiry_JSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cookies.json")
	// Минимальная дата — 1800000000 (2027-01-21), вторая — 1900000000
	content := `[
		{"domain": ".instagram.com", "path": "/", "expirationDate": 1900000000, "name": "a", "value": "1"},
		{"domain": ".instagram.com", "path": "/", "expirationDate": 1800000000, "name": "b", "value": "2"}
	]`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	expiry, err := CookieExpiry(path)
	if err != nil {
		t.Fatalf("CookieExpiry: %v", err)
	}
	expected := time.Unix(1800000000, 0)
	if !expiry.Equal(expected) {
		t.Errorf("ожидалась дата %v, получено %v", expected, expiry)
	}
}

func TestCookieExpiry_JSON_EmptyArray(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte("[]"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := CookieExpiry(path)
	if err == nil {
		t.Fatal("ожидалась ошибка для пустого JSON-массива")
	}
}

func TestCookieExpiry_JSON_NoExpirationDate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "noexp.json")
	content := `[{"domain": ".x.com", "path": "/", "name": "s", "value": "v"}]`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := CookieExpiry(path)
	if err == nil {
		t.Fatal("ожидалась ошибка при отсутствии дат истечения в JSON")
	}
}

func TestCookieExpiry_InvalidFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "invalid.txt")
	content := "not json and not netscape\nplain text"
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := CookieExpiry(path)
	if err == nil {
		t.Fatal("ожидалась ошибка для неверного формата (Netscape без валидных полей)")
	}
}
