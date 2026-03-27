// Пакет adminnotify — отправка уведомлений администратору в Telegram (cookies, ошибки Instagram).
package adminnotify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"download_track/internal/downloader"
)

// Notifier отправляет текстовые сообщения в чат администратора через Telegram Bot API.
type Notifier struct {
	token       string
	apiBase     string
	adminChatID string
	client      *http.Client
}

// New создаёт Notifier. Если adminChatID пустой, возвращает nil (уведомления отключены).
func New(token, apiBase, adminChatID string) *Notifier {
	if token == "" || adminChatID == "" {
		return nil
	}
	if apiBase == "" {
		apiBase = "https://api.telegram.org"
	}
	return &Notifier{
		token:       token,
		apiBase:     apiBase,
		adminChatID: adminChatID,
		client:      &http.Client{Timeout: 15 * time.Second},
	}
}

// NotifyAdmin отправляет сообщение msg в чат администратора (POST sendMessage).
// Возвращает true только при HTTP 200 и JSON {"ok":true}. Ошибки логируются.
func (n *Notifier) NotifyAdmin(msg string) bool {
	if n == nil {
		return false
	}
	url := fmt.Sprintf("%s/bot%s/sendMessage", n.apiBase, n.token)
	body := map[string]string{"chat_id": n.adminChatID, "text": msg}
	data, err := json.Marshal(body)
	if err != nil {
		log.Printf("adminnotify: marshal sendMessage: %v", err)
		return false
	}
	resp, err := n.client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("adminnotify: sendMessage request: %v", err)
		return false
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("adminnotify: sendMessage read body: %v", err)
		return false
	}
	if resp.StatusCode != http.StatusOK {
		log.Printf("adminnotify: sendMessage status %d body %s", resp.StatusCode, string(raw))
		return false
	}
	var tg struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(raw, &tg); err != nil {
		log.Printf("adminnotify: sendMessage json: %v", err)
		return false
	}
	if !tg.OK {
		log.Printf("adminnotify: sendMessage ok=false body %s", string(raw))
		return false
	}
	return true
}

// CheckCookiesFileAtStartup проверяет файл cookies при старте: при недоступности или ошибке
// парсинга отправляет админу уведомление. Истечение срока при старте не проверяется.
func CheckCookiesFileAtStartup(cookiesPath string, notifier *Notifier) {
	if notifier == nil || cookiesPath == "" {
		return
	}
	if _, err := os.Stat(cookiesPath); err != nil {
		_ = notifyWithRetry(notifier, "Файл cookies Instagram недоступен (не найден или не читается). Проверьте путь и том в docker-compose.")
		return
	}
	_, err := downloader.CookieExpiry(cookiesPath)
	if err != nil {
		_ = notifyWithRetry(notifier, "Файл cookies Instagram найден, но не удаётся определить срок истечения (неверный формат или пустой файл). Проверьте содержимое — ожидается Netscape или JSON.")
	}
}

// Пороги (дни до истечения), при которых отправляется уведомление.
const (
	thresholdDays7 = 7
	thresholdDays3 = 3
	thresholdDays1 = 1
)

// Формат даты для сообщений админу.
const expiryDateLayout = "02.01.2006"

// stateCookieCheck хранит, какие пороги уже отправлены для данной даты истечения.
type stateCookieCheck struct {
	mu          sync.Mutex
	expiry      time.Time
	sent7       bool
	sent3       bool
	sent1       bool
	sentExpired bool
}

// sleepForRetry подменяется в тестах, чтобы не ждать реальные паузы между попытками.
var sleepForRetry = time.Sleep

// notifyWithRetry вызывает отправку до трёх раз с паузами 2 с и 5 с между попытками.
func notifyWithRetry(n *Notifier, msg string) bool {
	if n == nil {
		return false
	}
	if n.NotifyAdmin(msg) {
		return true
	}
	sleepForRetry(2 * time.Second)
	if n.NotifyAdmin(msg) {
		return true
	}
	sleepForRetry(5 * time.Second)
	return n.NotifyAdmin(msg)
}

// formatDaysRu возвращает фразу вида «N дней» / «N дня» / «N день» для натурального n.
func formatDaysRu(n int) string {
	if n <= 0 {
		return "0 дней"
	}
	nAbs := n % 100
	if nAbs >= 11 && nAbs <= 14 {
		return fmt.Sprintf("%d дней", n)
	}
	switch n % 10 {
	case 1:
		return fmt.Sprintf("%d день", n)
	case 2, 3, 4:
		return fmt.Sprintf("%d дня", n)
	default:
		return fmt.Sprintf("%d дней", n)
	}
}

// runCookieExpiryIteration одна проверка файла cookies и при необходимости уведомление.
func runCookieExpiryIteration(cookiesPath string, notifier *Notifier, state *stateCookieCheck) {
	expiry, err := downloader.CookieExpiry(cookiesPath)
	if err != nil {
		return
	}
	now := time.Now()
	expiryDateStr := expiry.Format(expiryDateLayout)

	state.mu.Lock()
	if !state.expiry.Equal(expiry) {
		state.expiry = expiry
		state.sent7 = false
		state.sent3 = false
		state.sent1 = false
		state.sentExpired = false
	}

	// Истёкшие cookies: одно сообщение на дату истечения после успешной доставки.
	if !expiry.After(now) {
		if state.sentExpired {
			state.mu.Unlock()
			return
		}
		state.mu.Unlock()
		msg := fmt.Sprintf("Cookies Instagram истекли (%s). Обновите файл.", expiryDateStr)
		if notifyWithRetry(notifier, msg) {
			state.mu.Lock()
			if state.expiry.Equal(expiry) {
				state.sentExpired = true
			}
			state.mu.Unlock()
		}
		return
	}

	daysLeft := downloader.DaysLeftCeil(now, expiry)

	if daysLeft <= thresholdDays7 && !state.sent7 {
		state.mu.Unlock()
		msg := fmt.Sprintf("Остаётся %s до истечения cookies Instagram (%s).", formatDaysRu(daysLeft), expiryDateStr)
		if notifyWithRetry(notifier, msg) {
			state.mu.Lock()
			if state.expiry.Equal(expiry) {
				state.sent7 = true
				// Уже в зоне 3 / 1 дня — помечаем нижние пороги, чтобы не слать повтор на следующем тике.
				if daysLeft <= thresholdDays3 {
					state.sent3 = true
				}
				if daysLeft <= thresholdDays1 {
					state.sent1 = true
				}
			}
			state.mu.Unlock()
		}
		return
	}
	if daysLeft <= thresholdDays3 && !state.sent3 {
		state.mu.Unlock()
		msg := fmt.Sprintf("Остаётся %s до истечения cookies Instagram (%s).", formatDaysRu(daysLeft), expiryDateStr)
		if notifyWithRetry(notifier, msg) {
			state.mu.Lock()
			if state.expiry.Equal(expiry) {
				state.sent3 = true
				if daysLeft <= thresholdDays1 {
					state.sent1 = true
				}
			}
			state.mu.Unlock()
		}
		return
	}
	if daysLeft <= thresholdDays1 && !state.sent1 {
		state.mu.Unlock()
		msg := fmt.Sprintf("Остаётся %s до истечения cookies Instagram (%s).", formatDaysRu(daysLeft), expiryDateStr)
		if notifyWithRetry(notifier, msg) {
			state.mu.Lock()
			if state.expiry.Equal(expiry) {
				state.sent1 = true
			}
			state.mu.Unlock()
		}
		return
	}
	state.mu.Unlock()
}

// RunCookieExpiryCheck запускает фоновый цикл проверки срока cookies (например раз в сутки).
// При первом попадании в пороги 7, 3 или 1 день до истечения отправляет сообщение с фактическим остатком дней;
// при истечении — одно уведомление на дату истечения. Флаги «отправлено» выставляются только после успешной
// доставки в Telegram (с короткими повторами при сбое). Первая проверка выполняется сразу после старта.
// Вызов блокирующий — запускать в горутине.
func RunCookieExpiryCheck(cookiesPath string, notifier *Notifier, interval time.Duration) {
	if notifier == nil || cookiesPath == "" || interval <= 0 {
		return
	}
	state := &stateCookieCheck{}
	runCookieExpiryIteration(cookiesPath, notifier, state)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		runCookieExpiryIteration(cookiesPath, notifier, state)
	}
}
