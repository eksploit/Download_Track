package bot

import (
	"bytes"
	"context"
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
	"time"

	"download_track/internal/joblog"
)

// cookieStatusResponse совпадает с ответом GET /cookie-status http-service.
type cookieStatusResponse struct {
	Available  bool   `json:"available"`
	Expired    bool   `json:"expired"`
	ParseError bool   `json:"parse_error"`
	Expiry     string `json:"expiry"`
	DaysLeft   int    `json:"days_left"`
	Error      string `json:"error"`
}

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
	repo              *repo
	apiBase           string
	adminJobLogToken  string
}

func newService(repo *repo, apiBase, adminJobLogToken string) *service {
	return &service{repo: repo, apiBase: apiBase, adminJobLogToken: adminJobLogToken}
}

// jobLogHTTPResponse — тело ответа GET /job-log http-service (дублирует JSON, без импорта httpserver).
type jobLogHTTPResponse struct {
	Entries     []map[string]any `json:"entries"`
	Truncated   bool             `json:"truncated"`
	ParseErrors int              `json:"parse_errors"`
}

const defaultJobLogLines = 20

// GetJobLogPreview запрашивает хвост job-лога у http-service и возвращает текст для Telegram.
// При пустом adminJobLogToken в сервисе — ошибка (токен не настроен).
func (s *service) GetJobLogPreview(lines int) (string, error) {
	if strings.TrimSpace(s.adminJobLogToken) == "" {
		return "", errors.New("ADMIN_JOB_LOG_TOKEN не задан: задай тот же секрет, что у http-service, в .env и перезапусти бота")
	}
	if lines <= 0 {
		lines = defaultJobLogLines
	}
	if lines > joblog.MaxLinesLimit {
		lines = joblog.MaxLinesLimit
	}
	u := fmt.Sprintf("%s/job-log?lines=%d", strings.TrimRight(s.apiBase, "/"), lines)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+s.adminJobLogToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		// ok
	case http.StatusUnauthorized:
		return "", errors.New("http-service отклонил токен (401): проверь ADMIN_JOB_LOG_TOKEN")
	case http.StatusNotFound:
		return "", errors.New("файл job-лога не найден на http-service (404)")
	case http.StatusServiceUnavailable:
		return "", errors.New("чтение job-лога недоступно (503)")
	default:
		return "", fmt.Errorf("http-service: статус %s", resp.Status)
	}
	var body jobLogHTTPResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return formatJobLogForTelegram(body), nil
}

// formatJobLogForTelegram собирает человекочитаемый текст из ответа /job-log.
func formatJobLogForTelegram(body jobLogHTTPResponse) string {
	var b strings.Builder
	if len(body.Entries) == 0 {
		b.WriteString("Записей нет (файл пустой или хвост не содержит валидного JSON).")
	} else {
		var lastRID string
		for _, e := range body.Entries {
			rid := mapStr(e, "request_id")
			if rid != lastRID {
				if b.Len() > 0 {
					b.WriteByte('\n')
				}
				b.WriteString("[")
				b.WriteString(shortRID(rid))
				b.WriteString("]\n")
				lastRID = rid
			}
			b.WriteString(formatJobLogLine(e))
			b.WriteByte('\n')
		}
	}
	if body.Truncated {
		b.WriteString(fmt.Sprintf("\n(сервер ограничил число строк лимитом %d)\n", joblog.MaxLinesLimit))
	}
	if body.ParseErrors > 0 {
		b.WriteString(fmt.Sprintf("(строк с битым JSON, пропущено: %d)\n", body.ParseErrors))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatJobLogLine(m map[string]any) string {
	t := shortTime(mapStr(m, "time"))
	ev := mapStr(m, "event")
	lvl := mapStr(m, "level")
	rid := shortRID(mapStr(m, "request_id"))
	switch ev {
	case "video_pipeline":
		return fmt.Sprintf("%s %s %s %s%s url=%s fmt=%s probe=%s ytdlp=%s ffmpeg=%s",
			t, lvl, ev, rid, jobLogUserSuffix(m),
			truncElide(mapStr(m, "file_url"), 56),
			mapStr(m, "format"),
			msFmt(m, "probe_ms"), msFmt(m, "ytdlp_ms"), msFmt(m, "ffmpeg_ms"),
		)
	case "delivery":
		return fmt.Sprintf("%s %s %s %s%s stage=%s status=%s size=%s",
			t, lvl, ev, rid, jobLogUserSuffix(m),
			mapStr(m, "stage"), mapStr(m, "status"), mapStr(m, "size"),
		)
	default:
		msg := strings.TrimSpace(mapStr(m, "msg"))
		line := fmt.Sprintf("%s %s %s %s%s", t, lvl, ev, rid, jobLogUserSuffix(m))
		if msg != "" {
			line += " " + msg
		}
		return line
	}
}

// jobLogUserSuffix — uid, @username и tg из полей slog (если есть), для вывода в /logs.
func jobLogUserSuffix(m map[string]any) string {
	uid := mapStr(m, "user_id")
	un := strings.TrimPrefix(mapStr(m, "username"), "@")
	tg := mapStr(m, "telegram_id")
	var parts []string
	if uid != "" {
		parts = append(parts, "uid="+uid)
	}
	if un != "" {
		parts = append(parts, "@"+un)
	}
	if tg != "" {
		parts = append(parts, "tg="+tg)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func mapStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	case json.Number:
		return x.String()
	default:
		return fmt.Sprint(x)
	}
}

func msFmt(m map[string]any, key string) string {
	s := mapStr(m, key)
	if s == "" {
		return "—"
	}
	return s + "ms"
}

func shortTime(iso string) string {
	if iso == "" {
		return "??:??:??"
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if tt, err := time.Parse(layout, iso); err == nil {
			return tt.Local().Format("15:04:05")
		}
	}
	if len(iso) >= 19 {
		return iso[11:19]
	}
	return iso
}

func shortRID(rid string) string {
	if rid == "" {
		return "—"
	}
	if len(rid) <= 12 {
		return rid
	}
	return rid[:8] + "…"
}

func truncElide(s string, max int) string {
	if max <= 3 || len(s) <= max {
		return s
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
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

// GetCookieStatus запрашивает GET /cookie-status у http-service и возвращает сообщение для админа.
func (s *service) GetCookieStatus() (string, error) {
	resp, err := http.Get(s.apiBase + "/cookie-status")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var status cookieStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return "", err
	}
	if !status.Available {
		if status.ParseError {
			return "Файл cookies найден, но не удаётся определить срок истечения. Проверьте формат (Netscape или JSON).", nil
		}
		return "Файл cookies Instagram недоступен.", nil
	}
	if status.Expired {
		return fmt.Sprintf("Cookies Instagram истекли (%s).", status.Expiry), nil
	}
	if status.DaysLeft == 0 {
		return fmt.Sprintf("До истечения cookies Instagram: меньше 1 дня (%s).", status.Expiry), nil
	}
	return fmt.Sprintf("До истечения cookies Instagram: %d дн. (%s).", status.DaysLeft, status.Expiry), nil
}

func generateAPIKey() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
