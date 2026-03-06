package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"download_track/internal/urlutil"
)

// Fetcher возвращает локальный путь к файлу по URL и функцию очистки (удаление временного файла).
type Fetcher interface {
	Fetch(ctx context.Context, url string) (localPath string, cleanup func(), err error)
}

// DefaultFetcher реализует Fetcher: для видео-платформ (YouTube, Instagram) — yt-dlp, иначе HTTP GET.
type DefaultFetcher struct {
	HTTPClient   *http.Client
	YtDlpTimeout time.Duration
	// CookiesPath — необязательный путь к файлу cookies (Netscape format) для yt-dlp (например для Instagram).
	CookiesPath string
}

// NewDefaultFetcher создаёт Fetcher с таймаутом для yt-dlp (например 10 минут). cookiesPath может быть пустым.
func NewDefaultFetcher(ytDlpTimeout time.Duration, cookiesPath string) *DefaultFetcher {
	if ytDlpTimeout <= 0 {
		ytDlpTimeout = 10 * time.Minute
	}
	return &DefaultFetcher{
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		YtDlpTimeout: ytDlpTimeout,
		CookiesPath:  cookiesPath,
	}
}

// Fetch скачивает файл по URL: для видео-платформ вызывает yt-dlp, иначе HTTP GET. Возвращает путь и cleanup.
func (f *DefaultFetcher) Fetch(ctx context.Context, url string) (localPath string, cleanup func(), err error) {
	if urlutil.IsVideoPlatformURL(url) {
		return f.fetchYtDlp(ctx, url)
	}
	return f.fetchHTTP(ctx, url)
}

func (f *DefaultFetcher) fetchHTTP(ctx context.Context, url string) (string, func(), error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, fmt.Errorf("http request: %w", err)
	}
	resp, err := f.HTTPClient.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("http get: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("http status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "dl-*")
	if err != nil {
		return "", nil, fmt.Errorf("temp file: %w", err)
	}
	path := tmp.Name()
	_, err = io.Copy(tmp, resp.Body)
	if err != nil {
		tmp.Close()
		os.Remove(path)
		return "", nil, fmt.Errorf("http copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(path)
		return "", nil, fmt.Errorf("close temp: %w", err)
	}
	cleanup := func() { _ = os.Remove(path) }
	return path, cleanup, nil
}

// cookieJSON — структура одной записи в JSON-экспорте cookies (Chrome/EditThisCookie и т.п.).
type cookieJSON struct {
	Domain         string  `json:"domain"`
	Path           string  `json:"path"`
	Secure         bool    `json:"secure"`
	ExpirationDate float64 `json:"expirationDate"`
	Name           string  `json:"name"`
	Value          string  `json:"value"`
}

// cookiesPathForYtDlp возвращает путь к файлу cookies в формате Netscape. Если исходный файл в JSON — конвертирует во временный файл в dir.
func cookiesPathForYtDlp(cookiesPath, dir string) (string, error) {
	data, err := os.ReadFile(cookiesPath)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(data))
	// Уже Netscape: первая строка с # или домен с табами.
	if strings.HasPrefix(trimmed, "#") || (len(trimmed) > 0 && !strings.HasPrefix(trimmed, "[")) {
		return cookiesPath, nil
	}
	var list []cookieJSON
	if err := json.Unmarshal(data, &list); err != nil {
		return "", fmt.Errorf("cookies json: %w", err)
	}
	outPath := filepath.Join(dir, "cookies_netscape.txt")
	f, err := os.Create(outPath)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString("# Netscape HTTP Cookie File\n"); err != nil {
		return "", err
	}
	for _, c := range list {
		domain := c.Domain
		if domain == "" {
			domain = ".instagram.com"
		}
		includeSubdomains := "TRUE"
		if !strings.HasPrefix(domain, ".") {
			includeSubdomains = "FALSE"
		}
		pathVal := c.Path
		if pathVal == "" {
			pathVal = "/"
		}
		secure := "FALSE"
		if c.Secure {
			secure = "TRUE"
		}
		expiry := int64(c.ExpirationDate)
		if expiry == 0 {
			expiry = 2000000000 // далеко в будущем для сессионных
		}
		line := domain + "\t" + includeSubdomains + "\t" + pathVal + "\t" + secure + "\t" + strconv.FormatInt(expiry, 10) + "\t" + c.Name + "\t" + c.Value + "\n"
		if _, err := f.WriteString(line); err != nil {
			return "", err
		}
	}
	return outPath, nil
}

func (f *DefaultFetcher) fetchYtDlp(ctx context.Context, url string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ytdlp-*")
	if err != nil {
		return "", nil, fmt.Errorf("yt-dlp temp dir: %w", err)
	}
	// Таймаут для yt-dlp (длинные видео)
	runCtx, cancel := context.WithTimeout(ctx, f.YtDlpTimeout)
	defer cancel()

	sourceBase := filepath.Join(dir, "source")
	args := []string{
		"-o", sourceBase + ".%(ext)s",
		"--no-playlist",
		"--no-part",
		"--max-filesize", "1.9G",
		"-f", "bestvideo+bestaudio/best",
		"--merge-output-format", "mkv",
	}
	if f.CookiesPath != "" {
		if _, err := os.Stat(f.CookiesPath); err == nil {
			cookiesFile := f.CookiesPath
			if converted, err := cookiesPathForYtDlp(f.CookiesPath, dir); err == nil {
				cookiesFile = converted
			}
			args = append(args, "--cookies", cookiesFile)
		}
	}
	args = append(args, url)
	cmd := exec.CommandContext(runCtx, "yt-dlp", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("yt-dlp: %w (output: %s)", err, string(out))
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("yt-dlp read dir: %w", err)
	}
	var foundPath string
	var foundSize int64
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".json") {
			continue
		}
		ext := strings.ToLower(path.Ext(name))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".txt" {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if strings.HasPrefix(name, "source.") || foundPath == "" {
			if info.Size() > foundSize {
				foundPath = filepath.Join(dir, name)
				foundSize = info.Size()
			}
		}
	}
	if foundPath == "" {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("yt-dlp: файл не найден (output: %s)", string(out))
	}

	normalizedPath := filepath.Join(dir, "video.mp4")
	if err := transcodeForTelegramIOS(runCtx, foundPath, normalizedPath); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}

	cleanup := func() { _ = os.RemoveAll(dir) }
	return normalizedPath, cleanup, nil
}

func transcodeForTelegramIOS(ctx context.Context, inputPath, outputPath string) error {
	// MP4: H.264 baseline, yuv420p, AAC, CFR 30 fps, genpts и faststart для воспроизведения в Telegram iOS.
	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-y",
		"-fflags", "+genpts",
		"-i", inputPath,
		"-map", "0:v:0",
		"-map", "0:a:0?",
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2,fps=30",
		"-c:v", "libx264",
		"-profile:v", "baseline",
		"-level", "4.0",
		"-pix_fmt", "yuv420p",
		"-preset", "veryfast",
		"-crf", "23",
		"-c:a", "aac",
		"-b:a", "128k",
		"-ac", "2",
		"-ar", "44100",
		"-movflags", "+faststart",
		outputPath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg: %w (output: %s)", err, string(out))
	}
	info, err := os.Stat(outputPath)
	if err != nil || info.Size() == 0 {
		return fmt.Errorf("ffmpeg: output пустой")
	}
	return nil
}
