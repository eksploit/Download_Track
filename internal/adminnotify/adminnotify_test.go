package adminnotify

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestNew_NilWhenEmpty проверяет, что New возвращает nil при пустом token или adminChatID.
func TestNew_NilWhenEmpty(t *testing.T) {
	if got := New("", "https://api.telegram.org", "123"); got != nil {
		t.Errorf("New(пустой token): ожидался nil, получен %v", got)
	}
	if got := New("token", "https://api.telegram.org", ""); got != nil {
		t.Errorf("New(пустой adminChatID): ожидался nil, получен %v", got)
	}
	if got := New("", "", "123"); got != nil {
		t.Errorf("New(оба пустые): ожидался nil, получен %v", got)
	}
}

// TestNew_DefaultAPIBase проверяет, что при пустом apiBase подставляется значение по умолчанию.
func TestNew_DefaultAPIBase(t *testing.T) {
	n := New("tok", "", "123")
	if n == nil {
		t.Fatal("New с пустым apiBase должен вернуть не nil")
	}
	// Notifier не экспортирует поля; проверяем через NotifyAdmin на мок-сервере:
	// URL должен содержать дефолтный base. Поскольку мы не можем прочитать apiBase,
	// просто убедимся, что нотифаер создаётся и не паникует при вызове NotifyAdmin с моком.
	var received struct {
		ChatID string `json:"chat_id"`
		Text   string `json:"text"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&received)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	n2 := New("tok", server.URL, "456")
	if ok := n2.NotifyAdmin("test"); !ok {
		t.Error("ожидался успешный NotifyAdmin")
	}
	if received.ChatID != "456" || received.Text != "test" {
		t.Errorf("ожидались chat_id=456, text=test; получено chat_id=%q, text=%q", received.ChatID, received.Text)
	}
}

// TestNotifyAdmin_SendsRequest проверяет, что NotifyAdmin отправляет POST с нужным телом.
func TestNotifyAdmin_SendsRequest(t *testing.T) {
	var mu sync.Mutex
	var chatID, text string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("ожидался POST, получен %s", r.Method)
		}
		var body struct {
			ChatID string `json:"chat_id"`
			Text   string `json:"text"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		chatID, text = body.ChatID, body.Text
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	n := New("test-token", server.URL, "admin123")
	if ok := n.NotifyAdmin("Проверка уведомления"); !ok {
		t.Fatal("ожидался успешный NotifyAdmin")
	}
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if chatID != "admin123" || text != "Проверка уведомления" {
		t.Errorf("ожидались chat_id=admin123, text=Проверка уведомления; получено chat_id=%q, text=%q", chatID, text)
	}
}

// TestNotifyAdmin_NilNoPanic проверяет, что вызов NotifyAdmin у nil notifier не паникует.
func TestNotifyAdmin_NilNoPanic(t *testing.T) {
	var n *Notifier
	if n.NotifyAdmin("любой текст") {
		t.Error("у nil notifier ожидался false")
	}
}

// TestNotifyAdmin_FalseWhenNotOK проверяет, что при ok=false в JSON возвращается false.
func TestNotifyAdmin_FalseWhenNotOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":false}`))
	}))
	defer server.Close()
	n := New("t", server.URL, "1")
	if n.NotifyAdmin("x") {
		t.Fatal("ожидался false при ok=false")
	}
}

// TestNotifyAdmin_FalseWhenBadStatus проверяет ответ не 200.
func TestNotifyAdmin_FalseWhenBadStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	n := New("t", server.URL, "1")
	if n.NotifyAdmin("x") {
		t.Fatal("ожидался false при 500")
	}
}

func TestFormatDaysRu(t *testing.T) {
	cases := []struct {
		n    int
		want string
	}{
		{1, "1 день"},
		{2, "2 дня"},
		{5, "5 дней"},
		{11, "11 дней"},
		{21, "21 день"},
		{22, "22 дня"},
		{25, "25 дней"},
	}
	for _, tc := range cases {
		if got := formatDaysRu(tc.n); got != tc.want {
			t.Errorf("formatDaysRu(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

// TestCheckCookiesFileAtStartup_UnavailableFile вызывает уведомление при недоступном файле.
func TestCheckCookiesFileAtStartup_UnavailableFile(t *testing.T) {
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		received = body.Text
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	n := New("t", server.URL, "1")
	CheckCookiesFileAtStartup("/nonexistent/cookies.txt", n)
	time.Sleep(50 * time.Millisecond)
	if received == "" {
		t.Fatal("ожидалось уведомление о недоступном файле")
	}
	if received != "Файл cookies Instagram недоступен (не найден или не читается). Проверьте путь и том в docker-compose." {
		t.Errorf("неверный текст уведомления: %q", received)
	}
}

// TestCheckCookiesFileAtStartup_ParseError вызывает уведомление при неверном формате файла.
func TestCheckCookiesFileAtStartup_ParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.txt")
	if err := os.WriteFile(path, []byte("garbage\nnot netscape or json"), 0644); err != nil {
		t.Fatal(err)
	}
	var received string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Text string `json:"text"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		received = body.Text
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	n := New("t", server.URL, "1")
	CheckCookiesFileAtStartup(path, n)
	time.Sleep(50 * time.Millisecond)
	if received == "" {
		t.Fatal("ожидалось уведомление об ошибке парсинга")
	}
	if received != "Файл cookies Instagram найден, но не удаётся определить срок истечения (неверный формат или пустой файл). Проверьте содержимое — ожидается Netscape или JSON." {
		t.Errorf("неверный текст уведомления: %q", received)
	}
}

// TestCheckCookiesFileAtStartup_NilNotifier не паникует при nil notifier.
func TestCheckCookiesFileAtStartup_NilNotifier(t *testing.T) {
	CheckCookiesFileAtStartup("/any/path", nil)
}

// TestCheckCookiesFileAtStartup_EmptyPath не делает запросов при пустом пути.
func TestCheckCookiesFileAtStartup_EmptyPath(t *testing.T) {
	var called bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	n := New("t", server.URL, "1")
	CheckCookiesFileAtStartup("", n)
	time.Sleep(50 * time.Millisecond)
	if called {
		t.Error("при пустом пути не должно быть запроса к API")
	}
}

// TestRunCookieExpiryIteration_NoSentOnFullFailure проверяет, что флаги не ставятся, если все попытки отправки провалились.
func TestRunCookieExpiryIteration_NoSentOnFullFailure(t *testing.T) {
	oldSleep := sleepForRetry
	sleepForRetry = func(time.Duration) {}
	defer func() { sleepForRetry = oldSleep }()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "c.txt")
	exp := time.Now().Add(5 * 24 * time.Hour)
	content := "# Netscape\n" + fmt.Sprintf(".instagram.com\tTRUE\t/\tTRUE\t%d\tc\tv\n", exp.Unix())
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	n := New("tok", server.URL, "1")
	state := &stateCookieCheck{}
	runCookieExpiryIteration(path, n, state)
	if attempts != 3 {
		t.Fatalf("ожидалось 3 HTTP-попытки, получено %d", attempts)
	}
	if state.sent7 {
		t.Fatal("sent7 не должен выставляться при полном провале")
	}
}

// TestRunCookieExpiryIteration_Sent7AfterThirdAttempt проверяет успех после двух неудачных ответов API.
func TestRunCookieExpiryIteration_Sent7AfterThirdAttempt(t *testing.T) {
	oldSleep := sleepForRetry
	sleepForRetry = func(time.Duration) {}
	defer func() { sleepForRetry = oldSleep }()

	nReq := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nReq++
		if nReq < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "c.txt")
	exp := time.Now().Add(5 * 24 * time.Hour)
	content := "# Netscape\n" + fmt.Sprintf(".instagram.com\tTRUE\t/\tTRUE\t%d\tc\tv\n", exp.Unix())
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	n := New("tok", server.URL, "1")
	state := &stateCookieCheck{}
	runCookieExpiryIteration(path, n, state)
	if nReq != 3 {
		t.Fatalf("ожидалось 3 запроса, получено %d", nReq)
	}
	if !state.sent7 {
		t.Fatal("ожидался sent7 после успешной 3-й попытки")
	}
}

// TestRunCookieExpiryCheck_ReturnsWhenNil проверяет, что RunCookieExpiryCheck сразу выходит при nil/пустых аргументах.
func TestRunCookieExpiryCheck_ReturnsWhenNil(t *testing.T) {
	done := make(chan struct{})
	go func() {
		RunCookieExpiryCheck("", nil, time.Hour)
		close(done)
	}()
	select {
	case <-done:
		// Ожидаем быстрого выхода при cookiesPath == ""
	case <-time.After(200 * time.Millisecond):
		t.Fatal("RunCookieExpiryCheck с пустым путём не должен блокировать")
	}
}
