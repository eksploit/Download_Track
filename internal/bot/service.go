package bot

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// sendReq — тело запроса к HTTP API /send.
type sendReq struct {
	APIKey  string `json:"api_key"`
	FileURL string `json:"file_url"`
	Mode    string `json:"mode"`
}

var gmailBlockedExts = map[string]bool{
	".exe": true, ".msi": true, ".bat": true, ".cmd": true, ".com": true,
	".scr": true, ".js": true, ".vbs": true, ".ps1": true, ".sh": true,
}

// service содержит бизнес-операции бота поверх репозитория и конфигурации.
type service struct {
	repo    *repo
	apiBase string
}

func newService(repo *repo, apiBase string) *service {
	return &service{repo: repo, apiBase: apiBase}
}

func isGmailAddress(email string) bool {
	lower := strings.ToLower(email)
	return strings.HasSuffix(lower, "@gmail.com") || strings.HasSuffix(lower, "@googlemail.com")
}

func isGmailBlockedExt(fileURL string) bool {
	ext := strings.ToLower(path.Ext(path.Base(fileURL)))
	return gmailBlockedExts[ext]
}

// RegisterUser регистрирует пользователя по Telegram ID, имени и email; генерирует api_key.
// Возвращает ErrAlreadyRegistered, если telegram_id уже привязан.
func (s *service) RegisterUser(telegramID int64, username, email string) error {
	exists, err := s.repo.telegramExists(telegramID)
	if err != nil {
		return err
	}
	if exists {
		return ErrAlreadyRegistered
	}
	apiKey, err := generateAPIKey()
	if err != nil {
		return err
	}
	userID, err := s.repo.createUser(email, apiKey)
	if err != nil {
		return err
	}
	return s.repo.linkTelegramUser(telegramID, username, userID)
}

// IsTelegramRegistered проверяет, зарегистрирован ли telegram_id, и возвращает username при наличии.
func (s *service) IsTelegramRegistered(telegramID int64) (bool, string, error) {
	exists, err := s.repo.telegramExists(telegramID)
	if err != nil {
		return false, "", err
	}
	if !exists {
		return false, "", nil
	}
	username, err := s.repo.usernameByTelegramID(telegramID)
	if err != nil {
		return true, "", err
	}
	return true, username, nil
}

// RequestEmailChange создаёт заявку на смену email и возвращает текст сообщения для админа.
func (s *service) RequestEmailChange(telegramID int64, username, newEmail string) (requestID int64, adminMessage string, err error) {
	userID, oldEmail, err := s.repo.userIDAndEmailByTelegramID(telegramID)
	if err != nil {
		return 0, "", err
	}
	requestID, err = s.repo.createEmailChangeRequest(userID, telegramID, oldEmail, newEmail)
	if err != nil {
		return 0, "", err
	}
	adminMessage = fmt.Sprintf(
		"Заявка #%d от @%s (telegram_id=%d, user_id=%d):\n%s -> %s\n\nДля подтверждения:\n/approve_change %d\n\nДля отказа:\n/reject_change %d",
		requestID, username, telegramID, userID, oldEmail, newEmail, requestID, requestID,
	)
	return requestID, adminMessage, nil
}

// ListPendingEmailChanges возвращает список заявок на смену email со статусом pending.
func (s *service) ListPendingEmailChanges() ([]PendingEmailChangeRow, error) {
	return s.repo.listPendingEmailChangeRequests()
}

// ApproveEmailChange подтверждает заявку, обновляет email пользователя и статус заявки.
// Возвращает telegramID и newEmail для уведомления пользователя и админа. sql.ErrNoRows — заявка не найдена.
func (s *service) ApproveEmailChange(reqIDStr string) (telegramID int64, newEmail string, err error) {
	reqID, err := strconv.Atoi(reqIDStr)
	if err != nil {
		return 0, "", fmt.Errorf("некорректный id заявки: %w", err)
	}
	userID, telegramID, _, newEmail, status, err := s.repo.emailChangeRequestByID(reqID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, "", err
		}
		return 0, "", err
	}
	if status != "pending" {
		return 0, "", fmt.Errorf("заявка #%d уже обработана (status=%s)", reqID, status)
	}
	if err := s.repo.updateUserEmail(userID, newEmail); err != nil {
		return 0, "", err
	}
	if err := s.repo.updateEmailChangeRequestStatus(reqID, "approved"); err != nil {
		return 0, "", err
	}
	return telegramID, newEmail, nil
}

// RejectEmailChange отклоняет заявку. Возвращает telegramID и newEmail для уведомления.
func (s *service) RejectEmailChange(reqIDStr string) (telegramID int64, newEmail string, err error) {
	reqID, err := strconv.Atoi(reqIDStr)
	if err != nil {
		return 0, "", fmt.Errorf("некорректный id заявки: %w", err)
	}
	_, telegramID, _, newEmail, status, err := s.repo.emailChangeRequestByID(reqID)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, "", err
		}
		return 0, "", err
	}
	if status != "pending" {
		return 0, "", fmt.Errorf("заявка #%d уже обработана (status=%s)", reqID, status)
	}
	if err := s.repo.updateEmailChangeRequestStatus(reqID, "rejected"); err != nil {
		return 0, "", err
	}
	return telegramID, newEmail, nil
}

// GetEmailForTelegram возвращает email пользователя по telegram_id.
func (s *service) GetEmailForTelegram(telegramID int64) (string, error) {
	return s.repo.emailByTelegramID(telegramID)
}

// GetAPIKeyForTelegram возвращает api_key пользователя по telegram_id.
func (s *service) GetAPIKeyForTelegram(telegramID int64) (string, error) {
	userID, err := s.repo.userIDByTelegramID(telegramID)
	if err != nil {
		return "", err
	}
	return s.repo.apiKeyByUserID(userID)
}

// IsGmailBlockedDelivery возвращает true, если доставка на email для этого пользователя и файла
// не рекомендуется (Gmail и заблокированное расширение).
func (s *service) IsGmailBlockedDelivery(telegramID int64, fileURL string) (blocked bool, err error) {
	email, err := s.repo.emailByTelegramID(telegramID)
	if err != nil {
		return false, err
	}
	return isGmailAddress(email) && isGmailBlockedExt(fileURL), nil
}

// GmailBlockedExt возвращает расширение файла для сообщения пользователю (например ".exe").
func (s *service) GmailBlockedExt(fileURL string) string {
	return strings.ToLower(path.Ext(path.Base(fileURL)))
}

// CallSend вызывает HTTP API /send с api_key, file_url и mode.
func (s *service) CallSend(apiKey, fileURL, mode string) error {
	body, _ := json.Marshal(sendReq{
		APIKey:  apiKey,
		FileURL: fileURL,
		Mode:    mode,
	})
	resp, err := http.Post(s.apiBase+"/send", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errors.New("send http status " + resp.Status)
	}
	return nil
}

func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
