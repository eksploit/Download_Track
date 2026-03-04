package delivery

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
)

type TelegramDelivery struct {
    Token   string
    BaseURL string // например: http://telegram-bot-api:8081
    Logger  *log.Logger
    Client  *http.Client
}

type telegramSendDocumentRequest struct {
    ChatID   int64  `json:"chat_id"`
    Document string `json:"document"`
    Caption  string `json:"caption,omitempty"`
}

type telegramAPIResponse struct {
    Ok          bool            `json:"ok"`
    Description string          `json:"description"`
    Result      json.RawMessage `json:"result"`
}

func NewTelegramDelivery(token, baseURL string, logger *log.Logger) *TelegramDelivery {
    if baseURL == "" {
        baseURL = "http://telegram-bot-api:8081"
    }
    return &TelegramDelivery{
        Token:   token,
        BaseURL: baseURL,
        Logger:  logger,
        Client:  &http.Client{},
    }
}

func (d *TelegramDelivery) SendFile(ctx context.Context, user User, srcURL string) error {
    if user.TelegramID == 0 {
        return fmt.Errorf("telegram id is empty for user_id=%d", user.ID)
    }
    if d.Token == "" {
        return fmt.Errorf("telegram token is empty")
    }

    apiURL := fmt.Sprintf("%s/bot%s/sendDocument", d.BaseURL, d.Token)

    reqBody := telegramSendDocumentRequest{
        ChatID:   user.TelegramID,
        Document: srcURL,
        // Caption можно потом сделать кастомным
    }

    bodyBytes, err := json.Marshal(reqBody)
    if err != nil {
        return fmt.Errorf("marshal sendDocument request: %w", err)
    }

    d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=request\n", user.ID, user.TelegramID, srcURL, user.Mode)

    httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
    if err != nil {
        return fmt.Errorf("create request: %w", err)
    }
    httpReq.Header.Set("Content-Type", "application/json")

    resp, err := d.Client.Do(httpReq)
    if err != nil {
        d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=error error=%q\n",
            user.ID, user.TelegramID, srcURL, user.Mode, err.Error())
        return fmt.Errorf("telegram sendDocument request failed: %w", err)
    }
    defer resp.Body.Close()

    var apiResp telegramAPIResponse
    if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
        return fmt.Errorf("decode telegram response: %w", err)
    }

    if !apiResp.Ok {
        d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=api_error description=%q\n",
            user.ID, user.TelegramID, srcURL, user.Mode, apiResp.Description)
        return fmt.Errorf("telegram api error: %s", apiResp.Description)
    }

    d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=sent\n",
        user.ID, user.TelegramID, srcURL, user.Mode)

    return nil
}
