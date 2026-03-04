package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"path/filepath"
)

type TelegramDelivery struct {
	Token   string
	BaseURL string
	Logger  *log.Logger
	Client  *http.Client
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

	d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=request\n",
		user.ID, user.TelegramID, srcURL, user.Mode)

	// 1. Скачиваем файл во временный файл
	getResp, err := d.Client.Get(srcURL)
	if err != nil {
		d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=download_error error=%q\n",
			user.ID, user.TelegramID, srcURL, user.Mode, err.Error())
		return fmt.Errorf("download failed: %w", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=download_bad_status http_status=%d\n",
			user.ID, user.TelegramID, srcURL, user.Mode, getResp.StatusCode)
		return fmt.Errorf("download bad status: %d", getResp.StatusCode)
	}

	urlFileName := path.Base(srcURL)
	if urlFileName == "." || urlFileName == "/" || urlFileName == "" {
		urlFileName = "downloaded-file"
	}

	tmpFile, err := os.CreateTemp("", "tgdl-*-"+urlFileName)
	if err != nil {
		return fmt.Errorf("temp file create: %w", err)
	}
	defer func() {
		tmpFile.Close()
		os.Remove(tmpFile.Name())
	}()

	written, err := io.Copy(tmpFile, getResp.Body)
	if err != nil {
		return fmt.Errorf("download copy: %w", err)
	}

	d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=downloaded size=%d\n",
		user.ID, user.TelegramID, srcURL, user.Mode, written)

	// 2. Переходим в начало файла
	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek temp file: %w", err)
	}

	// 3. Отправляем через multipart/form-data
	apiURL := fmt.Sprintf("%s/bot%s/sendDocument", d.BaseURL, d.Token)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	// chat_id
	if err := writer.WriteField("chat_id", fmt.Sprintf("%d", user.TelegramID)); err != nil {
		return fmt.Errorf("write chat_id field: %w", err)
	}

	// document
	part, err := writer.CreateFormFile("document", filepath.Base(tmpFile.Name()))
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, tmpFile); err != nil {
		return fmt.Errorf("copy to multipart: %w", err)
	}

	writer.Close()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, &body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", writer.FormDataContentType())

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

	d.Logger.Printf("telegram delivery: user_id=%d telegram_id=%d url=%s mode=%s status=sent size=%d\n",
		user.ID, user.TelegramID, srcURL, user.Mode, written)

	return nil
}
