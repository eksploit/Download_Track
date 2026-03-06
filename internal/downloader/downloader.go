package downloader

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
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
}

// NewDefaultFetcher создаёт Fetcher с таймаутом для yt-dlp (например 10 минут).
func NewDefaultFetcher(ytDlpTimeout time.Duration) *DefaultFetcher {
	if ytDlpTimeout <= 0 {
		ytDlpTimeout = 10 * time.Minute
	}
	return &DefaultFetcher{
		HTTPClient:   &http.Client{Timeout: 30 * time.Second},
		YtDlpTimeout: ytDlpTimeout,
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

func (f *DefaultFetcher) fetchYtDlp(ctx context.Context, url string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ytdlp-*")
	if err != nil {
		return "", nil, fmt.Errorf("yt-dlp temp dir: %w", err)
	}
	// Таймаут для yt-dlp (длинные видео)
	runCtx, cancel := context.WithTimeout(ctx, f.YtDlpTimeout)
	defer cancel()

	sourceBase := filepath.Join(dir, "source")
	// Скачиваем лучшее качество в любом формате. Дальше нормализуем ffmpeg'ом в MP4 под iOS Telegram,
	// иначе возможна ситуация: звук есть, а видео «застыло» на первом кадре (при этом превью на ползунке меняется).
	cmd := exec.CommandContext(runCtx, "yt-dlp",
		"-o", sourceBase+".%(ext)s",
		"--no-playlist",
		"--no-part",
		"--max-filesize", "1.9G",
		"-f", "bestvideo+bestaudio/best",
		"--merge-output-format", "mkv",
		url,
	)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("yt-dlp: %w (output: %s)", err, string(out))
	}

	// Ищем скачанный файл в dir (обычно source.<ext>).
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
		// служебные файлы yt-dlp
		if strings.HasSuffix(name, ".part") || strings.HasSuffix(name, ".json") {
			continue
		}
		// исключаем очевидные не-видео
		ext := strings.ToLower(path.Ext(name))
		if ext == ".jpg" || ext == ".jpeg" || ext == ".png" || ext == ".webp" || ext == ".txt" {
			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}
		// предпочитаем source.* и берём самый большой файл (на случай нескольких артефактов)
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
	// Нормализация для iOS Telegram:
	// - H.264 baseline + yuv420p
	// - AAC, 2 канала
	// - фиксируем временные метки и FPS, чтобы избежать «застывшего» видео при работающем звуке
	// - moov atom в начале для быстрого старта
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
