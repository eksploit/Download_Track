package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"download_track/internal/logutil"
	"download_track/internal/requestid"
)

type TelegramDelivery struct {
	Token   string
	BaseURL string
	Logger  *slog.Logger
	Client  *http.Client
}

type telegramAPIResponse struct {
	Ok          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func NewTelegramDelivery(token, baseURL string, logger *slog.Logger) *TelegramDelivery {
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

func (d *TelegramDelivery) SendFile(ctx context.Context, user User, src string) error {
	if user.TelegramID == 0 {
		return fmt.Errorf("telegram id is empty for user_id=%d", user.ID)
	}
	if d.Token == "" {
		return fmt.Errorf("telegram token is empty")
	}

	ref := logutil.TruncateString(src, 256)
	d.Logger.InfoContext(ctx, "telegram delivery",
		slog.String("event", "delivery"),
		slog.String("channel", "telegram"),
		slog.String("stage", "request"),
		slog.String("request_id", requestid.From(ctx)),
		slog.Int("user_id", user.ID),
		slog.String("username", user.Username),
		slog.Int64("telegram_id", user.TelegramID),
		slog.String("url", ref),
		slog.String("mode", user.Mode),
		slog.String("status", "request"),
	)

	var file *os.File
	var size int64
	var fileName string
	var filePath string // путь к файлу на диске (для ffprobe при отправке видео)

	if isURL(src) {
		// src — URL: скачиваем во временный файл
		getResp, err := d.Client.Get(src)
		if err != nil {
			d.Logger.InfoContext(ctx, "telegram delivery",
				slog.String("event", "delivery"),
				slog.String("channel", "telegram"),
				slog.String("stage", "download_error"),
				slog.String("request_id", requestid.From(ctx)),
				slog.Int("user_id", user.ID),
				slog.String("username", user.Username),
				slog.Int64("telegram_id", user.TelegramID),
				slog.String("url", ref),
				slog.String("mode", user.Mode),
				slog.String("status", "download_error"),
				slog.String("error", err.Error()),
			)
			return fmt.Errorf("download failed: %w", err)
		}
		defer getResp.Body.Close()

		if getResp.StatusCode != http.StatusOK {
			d.Logger.InfoContext(ctx, "telegram delivery",
				slog.String("event", "delivery"),
				slog.String("channel", "telegram"),
				slog.String("stage", "download_bad_status"),
				slog.String("request_id", requestid.From(ctx)),
				slog.Int("user_id", user.ID),
				slog.String("username", user.Username),
				slog.Int64("telegram_id", user.TelegramID),
				slog.String("url", ref),
				slog.String("mode", user.Mode),
				slog.String("status", "download_bad_status"),
				slog.Int("http_status", getResp.StatusCode),
			)
			return fmt.Errorf("download bad status: %d", getResp.StatusCode)
		}

		urlFileName := path.Base(src)
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
		if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
			return fmt.Errorf("seek temp file: %w", err)
		}

		file = tmpFile
		size = written
		fileName = filepath.Base(tmpFile.Name())
		filePath = tmpFile.Name()
	} else {
		// src — путь к локальному файлу: открываем и отправляем
		filePath = src
		f, err := os.Open(src)
		if err != nil {
			d.Logger.InfoContext(ctx, "telegram delivery",
				slog.String("event", "delivery"),
				slog.String("channel", "telegram"),
				slog.String("stage", "open_error"),
				slog.String("request_id", requestid.From(ctx)),
				slog.Int("user_id", user.ID),
				slog.String("username", user.Username),
				slog.Int64("telegram_id", user.TelegramID),
				slog.String("url", ref),
				slog.String("mode", user.Mode),
				slog.String("status", "open_error"),
				slog.String("error", err.Error()),
			)
			return fmt.Errorf("open file: %w", err)
		}
		defer f.Close()

		info, err := f.Stat()
		if err != nil {
			return fmt.Errorf("stat file: %w", err)
		}
		size = info.Size()
		fileName = filepath.Base(src)
		if fileName == "." || fileName == "" {
			fileName = "file"
		}
		file = f
	}

	d.Logger.InfoContext(ctx, "telegram delivery",
		slog.String("event", "delivery"),
		slog.String("channel", "telegram"),
		slog.String("stage", "downloaded"),
		slog.String("request_id", requestid.From(ctx)),
		slog.Int("user_id", user.ID),
		slog.String("username", user.Username),
		slog.Int64("telegram_id", user.TelegramID),
		slog.String("url", ref),
		slog.String("mode", user.Mode),
		slog.String("status", "downloaded"),
		slog.Int64("size", size),
	)

	// Для видео используем sendVideo — воспроизведение в чате; для остальных — sendDocument
	useVideo := isVideoExtension(fileName)
	var endpoint, formField string
	if useVideo {
		endpoint = "sendVideo"
		formField = "video"
	} else {
		endpoint = "sendDocument"
		formField = "document"
	}
	apiURL := fmt.Sprintf("%s/bot%s/%s", d.BaseURL, d.Token, endpoint)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if err := writer.WriteField("chat_id", fmt.Sprintf("%d", user.TelegramID)); err != nil {
		return fmt.Errorf("write chat_id field: %w", err)
	}
	// Видео: параметры для воспроизведения в Telegram iOS (width/height/duration, supports_streaming, thumb).
	if useVideo {
		if err := writer.WriteField("supports_streaming", "true"); err != nil {
			return fmt.Errorf("write supports_streaming field: %w", err)
		}
		if filePath != "" {
			if w, h, dur := getVideoMeta(ctx, d.Logger, filePath); w > 0 && h > 0 {
				if err := writer.WriteField("width", strconv.Itoa(w)); err != nil {
					return fmt.Errorf("write width field: %w", err)
				}
				if err := writer.WriteField("height", strconv.Itoa(h)); err != nil {
					return fmt.Errorf("write height field: %w", err)
				}
				if dur > 0 {
					if err := writer.WriteField("duration", strconv.Itoa(dur)); err != nil {
						return fmt.Errorf("write duration field: %w", err)
					}
				}
			}
			if thumbPath, cleanup, err := makeVideoThumbnail(ctx, d.Logger, filePath); err == nil {
				defer cleanup()
				if thumbData, err := os.ReadFile(thumbPath); err == nil && len(thumbData) > 0 && len(thumbData) < maxThumbSize {
					thumbPart, _ := writer.CreateFormFile("thumb", "thumb.jpg")
					if thumbPart != nil {
						_, _ = thumbPart.Write(thumbData)
					}
				}
			}
		}
	}

	part, err := writer.CreateFormFile(formField, fileName)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(part, file); err != nil {
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
		d.Logger.InfoContext(ctx, "telegram delivery",
			slog.String("event", "delivery"),
			slog.String("channel", "telegram"),
			slog.String("stage", "http_error"),
			slog.String("request_id", requestid.From(ctx)),
			slog.Int("user_id", user.ID),
			slog.String("username", user.Username),
			slog.Int64("telegram_id", user.TelegramID),
			slog.String("url", ref),
			slog.String("mode", user.Mode),
			slog.String("status", "error"),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("telegram %s request failed: %w", endpoint, err)
	}
	defer resp.Body.Close()

	var apiResp telegramAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return fmt.Errorf("decode telegram response: %w", err)
	}

	if !apiResp.Ok {
		d.Logger.InfoContext(ctx, "telegram delivery",
			slog.String("event", "delivery"),
			slog.String("channel", "telegram"),
			slog.String("stage", "api_error"),
			slog.String("request_id", requestid.From(ctx)),
			slog.Int("user_id", user.ID),
			slog.String("username", user.Username),
			slog.Int64("telegram_id", user.TelegramID),
			slog.String("url", ref),
			slog.String("mode", user.Mode),
			slog.String("status", "api_error"),
			slog.String("description", apiResp.Description),
		)
		return fmt.Errorf("telegram api error: %s", apiResp.Description)
	}

	d.Logger.InfoContext(ctx, "telegram delivery",
		slog.String("event", "delivery"),
		slog.String("channel", "telegram"),
		slog.String("stage", "sent"),
		slog.String("request_id", requestid.From(ctx)),
		slog.Int("user_id", user.ID),
		slog.String("username", user.Username),
		slog.Int64("telegram_id", user.TelegramID),
		slog.String("url", ref),
		slog.String("mode", user.Mode),
		slog.String("status", "sent"),
		slog.Int64("size", size),
	)

	return nil
}

// Расширения видео: отправка через sendVideo для просмотра в чате, остальное — sendDocument.
var videoExtensions = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".mkv": true, ".webm": true,
	".avi": true, ".mpg": true, ".mpeg": true, ".3gp": true, ".ogv": true,
}

func isVideoExtension(fileName string) bool {
	ext := strings.ToLower(path.Ext(fileName))
	return videoExtensions[ext]
}

// ffprobe JSON для извлечения width, height, duration.
type ffprobeOutput struct {
	Streams []struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// getVideoMeta возвращает width, height и duration (сек) через ffprobe; при ошибке — 0, 0, 0.
func getVideoMeta(ctx context.Context, logger *slog.Logger, path string) (width, height, duration int) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(probeCtx, "ffprobe",
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height",
		"-show_entries", "format=duration",
		"-of", "json",
		path,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		if logger != nil {
			logger.WarnContext(ctx, "ffprobe",
				slog.String("path", logutil.TruncateString(path, 256)),
				slog.Any("err", err),
				slog.String("output", string(out)),
			)
		}
		return 0, 0, 0
	}
	var probe ffprobeOutput
	if err := json.Unmarshal(out, &probe); err != nil {
		return 0, 0, 0
	}
	if len(probe.Streams) == 0 {
		return 0, 0, 0
	}
	width = probe.Streams[0].Width
	height = probe.Streams[0].Height
	if probe.Format.Duration != "" {
		if f, err := strconv.ParseFloat(probe.Format.Duration, 64); err == nil && f > 0 {
			duration = int(f)
			if duration < 1 && f > 0 {
				duration = 1
			}
		}
	}
	return width, height, duration
}

const maxThumbSize = 200 * 1024 // Telegram: thumbnail < 200 kB
const maxThumbDim = 320         // Telegram: ширина и высота ≤ 320

// makeVideoThumbnail создаёт JPEG-превью (первый кадр, макс. maxThumbDim px, < maxThumbSize) через ffmpeg.
func makeVideoThumbnail(ctx context.Context, logger *slog.Logger, videoPath string) (thumbPath string, cleanup func(), err error) {
	tmp, err := os.CreateTemp("", "tgthumb-*.jpg")
	if err != nil {
		return "", nil, err
	}
	tmp.Close()
	thumbPath = tmp.Name()
	cleanup = func() { _ = os.Remove(thumbPath) }

	args := []string{
		"-y", "-i", videoPath,
		"-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", maxThumbDim, maxThumbDim),
		"-vframes", "1", "-q:v", "5",
		thumbPath,
	}
	ffCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ffCtx, "ffmpeg", args...)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		if logger != nil {
			logger.WarnContext(ctx, "ffmpeg thumbnail",
				slog.String("video_path", logutil.TruncateString(videoPath, 256)),
				slog.Any("err", runErr),
				slog.String("output", string(out)),
			)
		}
		return "", nil, runErr
	}
	info, err := os.Stat(thumbPath)
	if err != nil || info.Size() == 0 {
		return "", nil, fmt.Errorf("thumbnail empty or missing")
	}
	if info.Size() > maxThumbSize {
		_ = os.Remove(thumbPath)
		args = []string{"-y", "-i", videoPath,
			"-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", maxThumbDim, maxThumbDim),
			"-vframes", "1", "-q:v", "10", thumbPath}
		cmd = exec.CommandContext(ctx, "ffmpeg", args...)
		if _, runErr := cmd.CombinedOutput(); runErr != nil {
			return "", nil, runErr
		}
	}
	return thumbPath, cleanup, nil
}
