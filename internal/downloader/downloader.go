package downloader

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	CookiesPath  string
	// Минимальный интервал между стартами загрузок с Instagram (0 = отключено).
	InstagramMinInterval time.Duration
	// Пауза в секундах перед началом загрузки в yt-dlp (--sleep-interval), только для Instagram; 0 = не добавлять.
	YtDlpSleepInterval int

	instagramMu        sync.Mutex
	lastInstagramStart time.Time
}

// NewDefaultFetcher создаёт Fetcher. cookiesPath может быть пустым; instagramMinInterval 0 отключает ограничение.
func NewDefaultFetcher(ytDlpTimeout time.Duration, cookiesPath string, instagramMinInterval time.Duration, ytDlpSleepInterval int) *DefaultFetcher {
	if ytDlpTimeout <= 0 {
		ytDlpTimeout = 10 * time.Minute
	}
	return &DefaultFetcher{
		HTTPClient:           &http.Client{Timeout: 30 * time.Second},
		YtDlpTimeout:         ytDlpTimeout,
		CookiesPath:          cookiesPath,
		InstagramMinInterval: instagramMinInterval,
		YtDlpSleepInterval:   ytDlpSleepInterval,
	}
}

// Fetch скачивает файл по URL: для видео-платформ вызывает yt-dlp, иначе HTTP GET. Возвращает путь и cleanup.
func (f *DefaultFetcher) Fetch(ctx context.Context, url string) (localPath string, cleanup func(), err error) {
	if urlutil.IsVideoPlatformURL(url) {
		return f.fetchYtDlp(ctx, url)
	}
	return f.fetchHTTP(ctx, url)
}

// filenameFromURL извлекает и санитизирует имя файла из URL (path, без query).
// Если имя пустое или небезопасное — возвращает "downloaded-file".
func filenameFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "downloaded-file"
	}
	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		return "downloaded-file"
	}
	name = filepath.Clean(name)
	name = strings.TrimPrefix(name, ".."+string(os.PathSeparator))
	if name == ".." || name == "." {
		return "downloaded-file"
	}
	if strings.ContainsRune(name, os.PathSeparator) || strings.Contains(name, "/") {
		return "downloaded-file"
	}
	if len(name) > 200 {
		name = name[:200]
	}
	return name
}

func (f *DefaultFetcher) fetchHTTP(ctx context.Context, fileURL string) (string, func(), error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fileURL, nil)
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

	fileName := filenameFromURL(fileURL)
	dir, err := os.MkdirTemp("", "dl-*")
	if err != nil {
		return "", nil, fmt.Errorf("temp dir: %w", err)
	}
	localPath := filepath.Join(dir, fileName)
	tmp, err := os.Create(localPath)
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("temp file: %w", err)
	}
	_, err = io.Copy(tmp, resp.Body)
	if err != nil {
		tmp.Close()
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("http copy: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("close temp: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	return localPath, cleanup, nil
}

// cookieJSON — запись из JSON-экспорта cookies (Chrome/EditThisCookie и т.п.).
type cookieJSON struct {
	Domain         string  `json:"domain"`
	Path           string  `json:"path"`
	Secure         bool    `json:"secure"`
	ExpirationDate float64 `json:"expirationDate"`
	Name           string  `json:"name"`
	Value          string  `json:"value"`
}

// Порог размера (байты): при ориентировочном размере до videoSizeThreshold1080 скачиваем до 1080p, иначе до 720p.
const videoSizeThreshold1080 = 100 * 1024 * 1024 // 100 МБ

// Строки формата yt-dlp: ограничение по высоте кадра для снижения нагрузки на перекодирование (ffmpeg).
const (
	formatMax1080 = "bestvideo[height<=1080]+bestaudio/best[height<=1080]"
	formatMax720  = "bestvideo[height<=720]+bestaudio/best[height<=720]"
)

// ytdlpFormat — один элемент из yt-dlp --dump-single-json .formats[].
type ytdlpFormat struct {
	Height   *int   `json:"height"`
	Filesize *int64 `json:"filesize"`
	Vcodec   string `json:"vcodec"`
	Acodec   string `json:"acodec"`
}

// ytdlpInfo — корень JSON от yt-dlp --dump-single-json.
type ytdlpInfo struct {
	Formats []ytdlpFormat `json:"formats"`
}

// chooseFormatBySize запрашивает у yt-dlp метаданные (без скачивания), оценивает размер варианта до 1080p
// и возвращает formatMax1080 при размере ≤100 МБ, иначе formatMax720. При ошибке пробы возвращает formatMax720.
func (f *DefaultFetcher) chooseFormatBySize(ctx context.Context, url, dir string, isInstagram bool) string {
	probeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	args := []string{"--dump-single-json", "--no-playlist", "--no-download"}
	if isInstagram && f.YtDlpSleepInterval > 0 {
		args = append(args, "--sleep-interval", strconv.Itoa(f.YtDlpSleepInterval))
	}
	if isInstagram && f.CookiesPath != "" {
		if _, err := os.Stat(f.CookiesPath); err == nil {
			cookiesFile := f.CookiesPath
			if converted, err := cookiesPathForYtDlp(f.CookiesPath, dir); err == nil {
				cookiesFile = converted
			}
			args = append(args, "--cookies", cookiesFile)
		}
	}
	args = append(args, url)
	cmd := exec.CommandContext(probeCtx, "yt-dlp", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return formatMax720
	}
	var info ytdlpInfo
	if err := json.Unmarshal(out, &info); err != nil {
		return formatMax720
	}
	var bestVideoSize int64 = -1
	var bestVideoHeight int
	var bestAudioSize int64 = -1
	for _, fm := range info.Formats {
		hasVideo := fm.Vcodec != "" && fm.Vcodec != "none"
		hasAudio := fm.Acodec != "" && fm.Acodec != "none"
		h := 0
		if fm.Height != nil {
			h = *fm.Height
		}
		if hasVideo && h <= 1080 && h > bestVideoHeight {
			bestVideoHeight = h
			bestVideoSize = 0
			if fm.Filesize != nil {
				bestVideoSize = *fm.Filesize
			} else {
				bestVideoSize = -1
			}
		}
		if hasAudio {
			if fm.Filesize != nil && *fm.Filesize > bestAudioSize {
				bestAudioSize = *fm.Filesize
			}
		}
	}
	if bestVideoSize < 0 || bestAudioSize < 0 {
		return formatMax720
	}
	total := bestVideoSize + bestAudioSize
	if total <= videoSizeThreshold1080 {
		return formatMax1080
	}
	return formatMax720
}

// cookiesPathForYtDlp возвращает путь к файлу в формате Netscape. Если исходный файл в JSON — конвертирует во временный файл в dir.
func cookiesPathForYtDlp(cookiesPath, dir string) (string, error) {
	data, err := os.ReadFile(cookiesPath)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(string(data))
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
			expiry = 2000000000
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
	runCtx, cancel := context.WithTimeout(ctx, f.YtDlpTimeout)
	defer cancel()

	isInstagram := strings.Contains(url, "instagram.com")
	if isInstagram && f.InstagramMinInterval > 0 {
		f.instagramMu.Lock()
		now := time.Now()
		waitUntil := f.lastInstagramStart.Add(f.InstagramMinInterval)
		if !f.lastInstagramStart.IsZero() && now.Before(waitUntil) {
			needWait := time.Until(waitUntil)
			f.instagramMu.Unlock()
			for needWait > 0 {
				timer := time.NewTimer(min(needWait, time.Second))
				select {
				case <-ctx.Done():
					timer.Stop()
					os.RemoveAll(dir)
					return "", nil, ctx.Err()
				case <-timer.C:
					needWait = time.Until(waitUntil)
				}
			}
			f.instagramMu.Lock()
		}
		f.lastInstagramStart = time.Now()
		f.instagramMu.Unlock()
	}

	sourceBase := filepath.Join(dir, "source")
	formatStr := f.chooseFormatBySize(runCtx, url, dir, isInstagram)
	args := []string{
		"-o", sourceBase + ".%(ext)s",
		"--no-playlist",
		"--no-part",
		"--max-filesize", "1.9G",
		"-f", formatStr,
		"--merge-output-format", "mkv",
	}
	if isInstagram && f.YtDlpSleepInterval > 0 {
		args = append(args, "--sleep-interval", strconv.Itoa(f.YtDlpSleepInterval))
	}
	if isInstagram && f.CookiesPath != "" {
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
