// Пакет adminnotify — отправка уведомлений администратору в Telegram (cookies, ошибки Instagram).
package adminnotify

import (
	"bytes"
	"encoding/json"
	"fmt"
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
// Ошибки логируются, но не возвращаются, чтобы не прерывать основной поток.
func (n *Notifier) NotifyAdmin(msg string) {
	if n == nil {
		return
	}
	url := fmt.Sprintf("%s/bot%s/sendMessage", n.apiBase, n.token)
	body := map[string]string{"chat_id": n.adminChatID, "text": msg}
	data, err := json.Marshal(body)
	if err != nil {
		log.Printf("adminnotify: marshal sendMessage: %v", err)
		return
	}
	resp, err := n.client.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		log.Printf("adminnotify: sendMessage request: %v", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		log.Printf("adminnotify: sendMessage status %d", resp.StatusCode)
	}
}

// CheckCookiesFileAtStartup проверяет файл cookies при старте: при недоступности или ошибке
// парсинга отправляет админу уведомление. Истечение срока при старте не проверяется.
func CheckCookiesFileAtStartup(cookiesPath string, notifier *Notifier) {
	if notifier == nil || cookiesPath == "" {
		return
	}
	if _, err := os.Stat(cookiesPath); err != nil {
		notifier.NotifyAdmin("Файл cookies Instagram недоступен (не найден или не читается). Проверьте путь и том в docker-compose.")
		return
	}
	_, err := downloader.CookieExpiry(cookiesPath)
	if err != nil {
		notifier.NotifyAdmin("Файл cookies Instagram найден, но не удаётся определить срок истечения (неверный формат или пустой файл). Проверьте содержимое — ожидается Netscape или JSON.")
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
	mu       sync.Mutex
	expiry   time.Time
	sent7    bool
	sent3    bool
	sent1    bool
}

// RunCookieExpiryCheck запускает фоновый цикл проверки срока cookies (например раз в сутки).
// При первом попадании в пороги 7, 3 или 1 день до истечения отправляет соответствующее сообщение админу.
// При ошибке CookieExpiry цикл пропускается. Вызов блокирующий — запускать в горутине.
func RunCookieExpiryCheck(cookiesPath string, notifier *Notifier, interval time.Duration) {
	if notifier == nil || cookiesPath == "" || interval <= 0 {
		return
	}
	state := &stateCookieCheck{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		expiry, err := downloader.CookieExpiry(cookiesPath)
		if err != nil {
			continue
		}
		now := time.Now()
		if expiry.Before(now) {
			continue
		}
		daysLeft := int(expiry.Sub(now).Hours() / 24)
		expiryDateStr := expiry.Format(expiryDateLayout)

		state.mu.Lock()
		if !state.expiry.Equal(expiry) {
			state.expiry = expiry
			state.sent7 = false
			state.sent3 = false
			state.sent1 = false
		}
		if daysLeft <= thresholdDays7 && !state.sent7 {
			state.sent7 = true
			state.mu.Unlock()
			notifier.NotifyAdmin(fmt.Sprintf("Осталось 7 дней до истечения cookies Instagram (%s).", expiryDateStr))
			continue
		}
		if daysLeft <= thresholdDays3 && !state.sent3 {
			state.sent3 = true
			state.mu.Unlock()
			notifier.NotifyAdmin(fmt.Sprintf("Осталось 3 дня до истечения cookies Instagram (%s).", expiryDateStr))
			continue
		}
		if daysLeft <= thresholdDays1 && !state.sent1 {
			state.sent1 = true
			state.mu.Unlock()
			notifier.NotifyAdmin(fmt.Sprintf("Осталось 1 день до истечения cookies Instagram (%s).", expiryDateStr))
			continue
		}
		state.mu.Unlock()
	}
}
