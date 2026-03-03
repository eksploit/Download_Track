package bot

import (
    "bytes"
    "crypto/rand"
    "database/sql"
    "encoding/hex"
    "encoding/json"
    "errors"
    "fmt"
    "log"
    "net/http"
    "strconv"
    "strings"
    "time"

    tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

type Bot struct {
    api         *tgbotapi.BotAPI
    db          *sql.DB
    apiBase     string
    adminChatID int64
}

type sendReq struct {
    APIKey  string `json:"api_key"`
    FileURL string `json:"file_url"`
}

func New(api *tgbotapi.BotAPI, db *sql.DB, apiBase string, adminChatID int64) *Bot {
    return &Bot{
        api:         api,
        db:          db,
        apiBase:     apiBase,
        adminChatID: adminChatID,
    }
}

// Run — цикл чтения updates и вызова handleMessage.
func (b *Bot) Run() {
    u := tgbotapi.NewUpdate(0)
    u.Timeout = 60

    updates := b.api.GetUpdatesChan(u)

    log.Println("bot started")

    for update := range updates {
        if update.Message == nil {
            continue
        }
        b.handleMessage(update.Message)
    }
}

func (b *Bot) handleMessage(m *tgbotapi.Message) {
    if m == nil {
        return
    }

    chatID := m.Chat.ID
    text := strings.TrimSpace(m.Text)

    if strings.HasPrefix(text, "/start") {
        registered, username, err := b.isTelegramRegistered(m.From.ID)
        if err != nil {
            log.Println("isTelegramRegistered err:", err)
            b.send(chatID, "Внутренняя ошибка, попробуй позже.")
            return
        }

        if !registered {
            b.send(chatID, "Привет! Отправь /register email@example.com для регистрации, потом просто кидай ссылки на файлы.")
        } else {
            b.send(chatID, "Привет! @"+username+". Просто кидай ссылки на файлы.")
        }
        return
    }

    if strings.HasPrefix(text, "/register") {
        parts := strings.Fields(text)
        if len(parts) != 2 {
            b.send(chatID, "Использование: /register email@example.com")
            return
        }
        email := parts[1]

        if err := b.registerTelegramUser(m.From.ID, m.From.UserName, email); err != nil {
            log.Println("register err:", err)
            b.send(chatID, "Ошибка регистрации, попробуй позже.")
        } else {
            b.send(chatID, "Готово! Теперь просто пришли ссылку на файл.")
        }
        return
    }

    if strings.HasPrefix(text, "/help") {
        b.send(chatID, "Доступные команды:\n"+
            "/start - приветствие и проверка регистрации\n"+
            "/register email@example.com - регистрация\n"+
            "/change_email new_email@example.com - запрос на смену email\n"+
            "/send <ссылка> - отправить файл по ссылке на почту (можно просто прислать ссылку без команды)\n"+
            "/help - эта справка")
        if chatID == b.adminChatID {
            b.send(chatID, "Админские команды:\n"+
                "/approve_change <id> - подтвердить смену email\n"+
                "/reject_change <id> - отклонить смену email\n"+
                "/list_changes - показать все заявки")
        }

        return
    }

    if strings.HasPrefix(text, "/change_email") {
        parts := strings.Fields(text)
        if len(parts) != 2 {
            b.send(chatID, "Использование: /change_email new_email@example.com")
            return
        }
        newEmail := parts[1]

        if err := b.requestEmailChange(m.From.ID, m.From.UserName, newEmail); err != nil {
            log.Println("requestEmailChange err:", err)
            b.send(chatID, "Ошибка запроса на смену email, попробуй позже.")
        } else {
            b.send(chatID, "Запрос на смену email отправлен админу, ожидайте подтверждения.")
        }
        return
    }

    if strings.HasPrefix(text, "/approve_change") {
        parts := strings.Fields(text)
        if len(parts) != 2 {
            b.send(chatID, "Использование: /approve_change <request_id>")
            return
        }
        if err := b.approveEmailChange(chatID, parts[1]); err != nil {
            log.Println("approveEmailChange err:", err)
            b.send(chatID, "Ошибка подтверждения заявки: "+err.Error())
        }
        return
    }

    if strings.HasPrefix(text, "/reject_change") {
        parts := strings.Fields(text)
        if len(parts) != 2 {
            b.send(chatID, "Использование: /reject_change <request_id>")
            return
        }
        if err := b.rejectEmailChange(chatID, parts[1]); err != nil {
            log.Println("rejectEmailChange err:", err)
            b.send(chatID, "Ошибка отклонения заявки: "+err.Error())
        }
        return
    }

    if strings.HasPrefix(text, "/list_changes") {
        if err := b.listEmailChanges(chatID); err != nil {
            log.Println("listEmailChanges err:", err)
            b.send(chatID, "Ошибка получения списка заявок: "+err.Error())
        }
        return
    }

    url := extractFirstURL(m)
    if url == "" {
        b.send(chatID, "Не нашёл ссылку в сообщении.")
        return
    }

    apiKey, err := b.getAPIKeyForTelegram(m.From.ID)
    if err != nil {
        log.Println("get api key err:", err)
        b.send(chatID, "Ты ещё не зарегистрирован. Сначала сделай /register email@example.com")
        return
    }

    if err := b.callSend(apiKey, url); err != nil {
        log.Println("process url err:", err)
        b.send(chatID, "Ошибка обработки ссылки: "+err.Error())
    } else {
        b.send(chatID, "Ссылка отправлена в HTTP-сервис, он обработает файл и отправит на твою почту.")
    }
}

func (b *Bot) listEmailChanges(chatID int64) error {
    if chatID != b.adminChatID {
        return nil
    }

    rows, err := b.db.Query(
        `SELECT id, user_id, old_email, new_email, status, created_at
         FROM email_change_requests
         WHERE status = 'pending'
         ORDER BY
            created_at DESC,
            id DESC`,
    )
    if err != nil {
        return err
    }
    defer rows.Close()

    var sb strings.Builder
    sb.WriteString("Заявки на смену email:\nДля подтверждения: /approve_change x\nДля отказа: /reject_change x\n")

    found := false
    for rows.Next() {
        found = true
        var (
            id       int64
            userID   int
            oldEmail string
            newEmail string
            status   string
            createdAt time.Time
        )
        if err := rows.Scan(&id, &userID, &oldEmail, &newEmail, &status, &createdAt); err != nil {
            return err
        }

        sb.WriteString(fmt.Sprintf(
            "#%d user_id=%d [%s]\n%s -> %s\n\n",
            id, userID, status, oldEmail, newEmail,
        ))
    }
    if err := rows.Err(); err != nil {
        return err
    }

    if !found {
        b.send(chatID, "Заявок на смену email пока нет.")
        return nil
    }

    b.send(chatID, sb.String())
    return nil
}

func (b *Bot) approveEmailChange(chatID int64, reqIDStr string) error {
    if chatID != b.adminChatID {
        return nil
    }

    reqID, err := strconv.Atoi(reqIDStr)
    if err != nil {
        b.send(chatID, "Некорректный id заявки.")
        return nil
    }

    var (
        userID     int
        telegramID int64
        oldEmail   string
        newEmail   string
        status     string
    )

    err = b.db.QueryRow(
        `SELECT user_id, telegram_id, old_email, new_email, status
         FROM email_change_requests
         WHERE id = $1`,
        reqID,
    ).Scan(&userID, &telegramID, &oldEmail, &newEmail, &status)
    if err != nil {
        if err == sql.ErrNoRows {
            b.send(chatID, "Заявка не найдена.")
            return nil
        }
        return err
    }

    if status != "pending" {
        b.send(chatID, fmt.Sprintf("Заявка #%d уже обработана (status=%s).", reqID, status))
        return nil
    }

    _, err = b.db.Exec(
        `UPDATE users SET email = $1 WHERE id = $2`,
        newEmail, userID,
    )
    if err != nil {
        return err
    }

    _, err = b.db.Exec(
        `UPDATE email_change_requests
         SET status = 'approved', processed_at = now()
         WHERE id = $1`,
        reqID,
    )
    if err != nil {
        return err
    }

    b.send(telegramID, fmt.Sprintf("Админ сменил твой email на %s.", newEmail))
    b.send(chatID, fmt.Sprintf("Заявка #%d подтверждена, email пользователя обновлён на %s.", reqID, newEmail))

    return nil
}

func (b *Bot) rejectEmailChange(chatID int64, reqIDStr string) error {
    if chatID != b.adminChatID {
        return nil
    }

    reqID, err := strconv.Atoi(reqIDStr)
    if err != nil {
        b.send(chatID, "Некорректный id заявки.")
        return nil
    }

    var (
        userID     int
        telegramID int64
        oldEmail   string
        newEmail   string
        status     string
    )

    err = b.db.QueryRow(
        `SELECT user_id, telegram_id, old_email, new_email, status
         FROM email_change_requests
         WHERE id = $1`,
        reqID,
    ).Scan(&userID, &telegramID, &oldEmail, &newEmail, &status)
    if err != nil {
        if err == sql.ErrNoRows {
            b.send(chatID, "Заявка не найдена.")
            return nil
        }
        return err
    }

    if status != "pending" {
        b.send(chatID, fmt.Sprintf("Заявка #%d уже обработана (status=%s).", reqID, status))
        return nil
    }

    _, err = b.db.Exec(
        `UPDATE email_change_requests
         SET status = 'rejected', processed_at = now()
         WHERE id = $1`,
        reqID,
    )
    if err != nil {
        return err
    }

    b.send(telegramID, fmt.Sprintf("Админ отклонил смену email на %s.", newEmail))
    b.send(chatID, fmt.Sprintf("Заявка #%d отклонена.", reqID))

    return nil
}

func (b *Bot) send(chatID int64, text string) {
    msg := tgbotapi.NewMessage(chatID, text)
    if _, err := b.api.Send(msg); err != nil {
        log.Println("send msg err:", err)
    }
}

func (b *Bot) registerTelegramUser(telegramID int64, username, email string) error {
    var exists bool
    err := b.db.QueryRow("SELECT EXISTS(SELECT 1 FROM telegram_users WHERE telegram_id=$1)", telegramID).Scan(&exists)
    if err != nil {
        return err
    }
    if exists {
        return nil
    }

    apiKey, err := generateAPIKey()
    if err != nil {
        return err
    }

    var userID int
    err = b.db.QueryRow(
        "INSERT INTO users (email, api_key) VALUES ($1,$2) RETURNING id",
        email, apiKey,
    ).Scan(&userID)
    if err != nil {
        return err
    }

    _, err = b.db.Exec(
        "INSERT INTO telegram_users (telegram_id, username, user_id) VALUES ($1,$2,$3)",
        telegramID, username, userID,
    )
    return err
}

func (b *Bot) getAPIKeyForTelegram(telegramID int64) (string, error) {
    var userID int
    err := b.db.QueryRow("SELECT user_id FROM telegram_users WHERE telegram_id=$1", telegramID).Scan(&userID)
    if err != nil {
        return "", err
    }

    var apiKey string
    err = b.db.QueryRow("SELECT api_key FROM users WHERE id=$1", userID).Scan(&apiKey)
    if err != nil {
        return "", err
    }
    return apiKey, nil
}

func (b *Bot) callSend(apiKey, fileURL string) error {
    body, _ := json.Marshal(sendReq{
        APIKey:  apiKey,
        FileURL: fileURL,
    })

    resp, err := http.Post(b.apiBase+"/send", "application/json", bytes.NewReader(body))
    if err != nil {
        return err
    }
    defer resp.Body.Close()
    if resp.StatusCode != http.StatusOK {
        return errors.New("send http status " + resp.Status)
    }
    return nil
}

func (b *Bot) isTelegramRegistered(telegramID int64) (bool, string, error) {
    var exists bool
    var username string

    err := b.db.QueryRow(
        "SELECT EXISTS(SELECT 1 FROM telegram_users WHERE telegram_id=$1)",
        telegramID,
    ).Scan(&exists)
    if err != nil {
        return false, "", err
    }
    if !exists {
        return false, "", nil
    }

    err = b.db.QueryRow(
        "SELECT t.username FROM telegram_users t WHERE t.telegram_id=$1",
        telegramID,
    ).Scan(&username)
    if err != nil {
        return true, "", err
    }

    return true, username, nil
}

func (b *Bot) requestEmailChange(telegramID int64, username string, newEmail string) error {
    var userID int
    var oldEmail string

    err := b.db.QueryRow(
        `SELECT u.id, u.email
         FROM telegram_users t
         JOIN users u ON u.id = t.user_id
         WHERE t.telegram_id = $1`,
        telegramID,
    ).Scan(&userID, &oldEmail)
    if err != nil {
        return err
    }

    var requestID int64
    err = b.db.QueryRow(
        `INSERT INTO email_change_requests (user_id, telegram_id, old_email, new_email, status)
         VALUES ($1, $2, $3, $4, 'pending')
         RETURNING id`,
        userID, telegramID, oldEmail, newEmail,
    ).Scan(&requestID)
    if err != nil {
        return err
    }

    var sb strings.Builder
    sb.WriteString("Заявка #")
    sb.WriteString(fmt.Sprint(requestID))
    sb.WriteString(" от @")
    sb.WriteString(username)
    sb.WriteString(" (telegram_id=")
    sb.WriteString(fmt.Sprint(telegramID))
    sb.WriteString(", user_id=")
    sb.WriteString(fmt.Sprint(userID))
    sb.WriteString("):\n")
    sb.WriteString(oldEmail)
    sb.WriteString(" -> ")
    sb.WriteString(newEmail)
    sb.WriteString("\n\nДля подтверждения:\n/approve_change ")
    sb.WriteString(fmt.Sprint(requestID))
    sb.WriteString("\n\nДля отказа:\n/reject_change ")
    sb.WriteString(fmt.Sprint(requestID))

    msg := tgbotapi.NewMessage(b.adminChatID, sb.String())
    if _, err := b.api.Send(msg); err != nil {
        return err
    }

    return nil
}

func extractFirstURL(m *tgbotapi.Message) string {
    if m == nil {
        return ""
    }

    runes := []rune(m.Text)

    for _, e := range m.Entities {
        if e.IsURL() {
            start := e.Offset
            end := e.Offset + e.Length
            if start < 0 || end > len(runes) {
                continue
            }
            return string(runes[start:end])
        }

        if e.IsTextLink() {
            u, err := e.ParseURL()
            if err != nil {
                continue
            }
            return u.String()
        }
    }

    return ""
}

func generateAPIKey() (string, error) {
    b := make([]byte, 32)
    if _, err := rand.Read(b); err != nil {
        return "", err
    }
    return hex.EncodeToString(b), nil
}
