## Архитектура проекта `Download Track Bot`

Этот документ описывает общую архитектуру проекта, взаимодействие компонентов и основные потоки данных.

---

## Обзор

Проект состоит из двух независимых бинарей:

- **`bot`**: Telegram‑бот, принимающий команды и ссылки от пользователей.
- **`http-service`**: HTTP‑сервис, который по API‑ключу и URL файла скачивает его и доставляет пользователю (email, Telegram или оба варианта).

Оба сервиса используют **общую PostgreSQL базу данных** и обмениваются данными через HTTP‑запрос `POST /send`.

---

## Структура каталогов

- **`cmd/bot`**  
  Точка входа Telegram‑бота.

- **`cmd/http-service`**  
  Точка входа HTTP‑сервиса доставки.

- **`internal/urlutil`**  
  Утилиты для работы с URL:
  - `IsVideoPlatformURL` — определяет ссылки YouTube/Instagram, которые нужно обрабатывать через yt-dlp.

- **`internal/bot`**  
  Логика Telegram‑бота (разбита по файлам):
  - `bot.go` — структура `Bot`, конструктор `New`, метод `Run` (цикл обновлений);
  - `handlers.go` — `handleMessage`, `handleCallbackQuery`, `extractFirstURL`, вспомогательные методы;
  - `repository.go` — слой доступа к БД (`repo`), работа с таблицами `users`, `telegram_users`, `email_change_requests`;
  - `service.go` — бизнес-операции поверх репозитория (регистрация, смена email, вызов HTTP `/send`, проверка Gmail-расширений).
  Обработка команд (`/start`, `/register`, `/change_email`, `/help`, админские), inline‑клавиатура и запрос в HTTP‑сервис (`POST /send`).

- **`internal/httpserver`**  
  HTTP‑слой:
  - маршруты `/health` и `/send`;
  - валидация входящего запроса;
  - поиск пользователя по `api_key` в БД;
  - выбор реализации доставки по полю `mode`.

- **`internal/downloader`**  
  Слой скачивания по URL:
  - `Fetch(ctx, url)` возвращает путь к временному файлу и `cleanup` для удаления;
  - для обычных URL — HTTP GET во временный файл;
  - для YouTube/Instagram — yt-dlp (на загрузку одного файла отводится до 10 минут; если за это время загрузка не завершилась, она отменяется), затем нормализация ffmpeg в MP4 для корректного воспроизведения в Telegram iOS.
  - **Ограничение разрешения по размеру**: перед скачиванием вызывается yt-dlp `--dump-single-json` (без загрузки); по списку форматов оценивается размер варианта «лучшее видео до 1080p + лучшее аудио». Если суммарный размер ≤100 МБ — используется формат до 1080p, иначе до 720p (`chooseFormatBySize`). Так снижается нагрузка на ffmpeg и риск таймаута на слабом CPU.
  - **Нагрузка на CPU**: перекодирование тяжёлого видео (4K, 60 fps, AV1) на одном ядре может идти в 5–10 раз медленнее реального времени (например, speed=0.16× и ~28 минут на ролик 4:39). Для более высокого качества без таймаутов нужен более мощный процессор или увеличение таймаута.
  - Опционально для Instagram: путь к файлу cookies (`CookiesPath`, задаётся через `YTDLP_COOKIES_PATH`). Для ссылок на Instagram yt-dlp вызывается с `--cookies`; поддерживаются форматы Netscape и JSON (конвертация в `cookiesPathForYtDlp`). Минимальный интервал между стартами загрузок (`InstagramMinInterval`) и пауза `--sleep-interval` (`YtDlpSleepInterval`) снижают риск rate limit.

- **`internal/delivery`**  
  Слой доставки файлов:
  - `delivery.go` — интерфейс `Delivery` и тип `User`;
  - `email.go` — реализация `EmailDelivery` через SMTP;
  - `telegram.go` — реализация `TelegramDelivery` через локальный Telegram Bot API;
  - `multi.go` — `MultiDelivery`, агрегирующая email и Telegram‑доставку.

---

## Telegram‑бот (`cmd/bot`, `internal/bot`)

### Точка входа `cmd/bot/main.go`

- Читает переменные окружения:
  - `TELEGRAM_TOKEN` — токен бота;
  - `API_BASE` — базовый URL HTTP‑сервиса (по умолчанию `http://http-service:8080`);
  - `DB_DSN` — строка подключения к PostgreSQL;
  - `ADMIN_CHAT_ID` — Telegram‑ID администратора.
- Инициализирует:
  - подключение к PostgreSQL (`sql.DB`);
  - клиент Telegram Bot API (`tgbotapi.BotAPI`);
  - меню команд для пользователей и отдельное меню для админа (через `SetMyCommands` и chat‑scope).
- Создаёт `bot.Bot` и вызывает `Run()`.

### Структура `Bot` и цикл обработки

`internal/bot/bot.go` определяет:

- `type Bot struct { api *tgbotapi.BotAPI; svc *service; adminChatID int64; pendingLinks map[int64]string }` — зависимости к БД и API вынесены в `service` и репозиторий внутри пакета.
- Метод `Run()`:
  - открывает `GetUpdatesChan`;
  - в цикле обрабатывает:
    - `CallbackQuery` через `handleCallbackQuery` (в `handlers.go`);
    - обычные сообщения через `handleMessage` (в `handlers.go`).

### Обработка сообщений

`handleMessage`:

- Разбор текстовых команд:
  - `/start` — проверка регистрации через `svc.IsTelegramRegistered`, приветствие;
  - `/register email@example.com` — регистрация через `svc.RegisterUser` (проверка дубликата, генерация API‑ключа, запись в `users` и `telegram_users`);
  - `/help` — вывод справки по командам, в т.ч. админским;
  - `/change_email new@example.com` — заявка на смену email через `svc.RequestEmailChange`, уведомление админа;
  - `/approve_change <id>`, `/reject_change <id>`, `/list_changes` — админские операции через сервис и репозиторий (`email_change_requests`).

- При отсутствии команды:
  - из сообщения извлекается первая URL‑ссылка (`extractFirstURL`);
  - проверяется, что пользователь зарегистрирован;
  - если ссылка относится к YouTube/Instagram (`urlutil.IsVideoPlatformURL`), бот:
    - сообщает «Скачивание началось…»;
    - вызывает `POST /send` с `mode=telegram` без клавиатуры;
    - видео приходит в этот чат после скачивания;
  - иначе ссылка сохраняется в `pendingLinks[telegramID]` и пользователю отправляется inline‑клавиатура:
    - «На email» (`delivery_email`);
    - «В этот чат» (`delivery_telegram`);
    - «И туда, и туда» (`delivery_both`).

### Обработка inline‑клавиатуры

`handleCallbackQuery`:

- Берёт сохранённую ссылку из `pendingLinks` по `telegramID`.
- Определяет режим:
  - `email`, `telegram`, `both`.
- Для email/оба режима:
  - получает email пользователя;
  - если email на Gmail и расширение файла из списка блокируемых (`.exe`, `.bat`, `.js` и т.п.), бот предупреждает и не отправляет запрос в HTTP‑сервис.
- Получает `api_key` пользователя через `svc.GetAPIKeyForTelegram`.
- Отправляет пользователю текстовое подтверждение выбранного режима.
- Вызывает HTTP‑сервис через `svc.CallSend`:
  - `POST {API_BASE}/send` с JSON `{ api_key, file_url, mode }`.
- Очищает `pendingLinks` и отвечает на `CallbackQuery`, чтобы убрать «часики» в Telegram.

---

## HTTP‑сервис (`cmd/http-service`, `internal/httpserver`, `internal/delivery`)

### Точка входа `cmd/http-service/main.go`

- Читает переменные окружения:
  - `DB_DSN` — строка подключения к PostgreSQL;
  - `SMTP_HOST`, `SMTP_PORT`, `SMTP_USER`, `SMTP_PASS`, `SMTP_FROM` — параметры SMTP;
  - `TELEGRAM_TOKEN` — токен Telegram‑бота для доставки;
  - `TELEGRAM_API_BASE` — базовый URL локального Telegram Bot API (по умолчанию `http://telegram-bot-api:8081`);
  - `YTDLP_COOKIES_PATH` — необязательный путь к файлу cookies для Instagram (внутри контейнера);
  - `INSTAGRAM_MIN_INTERVAL_SECONDS`, `YTDLP_SLEEP_INTERVAL_SECONDS` — ограничение частоты загрузок с Instagram (интервал между стартами и пауза перед началом).
- Инициализирует:
  - подключение к БД;
  - файловый логгер в `/logs/send.log`;
  - `EmailDelivery` с конфигурацией SMTP;
  - `TelegramDelivery` (если задан `TELEGRAM_TOKEN`);
  - `MultiDelivery` для режима `both`.
- Создаёт `httpserver.Server`, регистрирует маршруты и запускает `http.Server` на `:8080`.

### HTTP‑слой (`internal/httpserver/server.go`)

`Server` содержит:

- `db *sql.DB` — доступ к БД;
- `jobLog *log.Logger` — логгер доставки;
- `fetcher downloader.Fetcher` — слой скачивания (обычный HTTP или yt-dlp);
- `emailDelivery`, `telegramDelivery`, `bothDelivery` — реализации `Delivery`.

Роуты:

- `GET /health` — простая проверка живости.
- `POST /send` — основной эндпоинт:
  - декодирует JSON `sendRequest { api_key, file_url, mode }`;
  - по `api_key` находит пользователя, его Telegram‑account и email;
  - по полю `mode` выбирает реализацию:
    - `email` → `emailDelivery`;
    - `telegram` → `telegramDelivery`;
    - `both` → `bothDelivery`.
  - скачивает URL через `fetcher.Fetch(ctx, file_url)` → `localPath`, `cleanup`;
  - `defer cleanup()` удаляет временный файл после доставки;
  - формирует `delivery.User` и вызывает `SendFile(ctx, user, localPath)`.
  - при ошибках отдаёт соответствующие HTTP‑коды (`401`, `400`, `500`, `502`).

---

## Слой доставки (`internal/delivery`)

### Общие типы

- `type User struct { ID int; Email string; TelegramID int64; Username string; Mode string }`
- `type Delivery interface { SendFile(ctx context.Context, user User, src string) error }` — `src` это путь к локальному файлу после `downloader.Fetch`.

### Email‑доставка (`EmailDelivery`, `email.go`)

- Отвечает за:
  - чтение вложения из локального файла по пути `src`;
  - построение MIME‑письма с вложением (через `github.com/scorredoira/email`);
  - отправку письма через SMTP (с поддержкой `STARTTLS`).
- Логирует статусы:
  - `received`, `downloading`, `downloaded`, `send_error`, `sent` с указанием `user_id`, `username`, `mode`, размера файла и т.д.

### Telegram‑доставка (`TelegramDelivery`, `telegram.go`)

- Отвечает за:
  - отправку локального файла в Telegram‑чат пользователя через локальный Telegram Bot API:
    - для видео — `sendVideo` (встроенный плеер в чате), иначе `sendDocument`;
    - дополнительно выставляются `supports_streaming`, `width/height/duration` (через ffprobe) и `thumb` (JPEG‑превью через ffmpeg) для лучшей совместимости iOS.
- Использует локальный Telegram Bot API контейнер, который уже инкапсулирует MTProto‑взаимодействие с Telegram.
- Логирует статусы `request`, `download_error`, `downloaded`, `api_error`, `sent`.

### Совмещённая доставка (`MultiDelivery`, `multi.go`)

- Оборачивает две реализации `Delivery`:
  - `Email`;
  - `Telegram`.
- В `SendFile`:
  - вызывает обе доставки (если они настроены);
  - возвращает ошибку, если обе упали, либо единственную ошибку, если один канал не сработал.

---

## Хранение данных

PostgreSQL используется минимум для следующих сущностей:

- **`users`** — пользователи сервиса:
  - `id`, `email`, `api_key` и др.
- **`telegram_users`** — связь Telegram‑аккаунта с пользователем:
  - `telegram_id`, `username`, `user_id`.
- **`email_change_requests`** — заявки на смену email:
  - `id`, `user_id`, `telegram_id`, `old_email`, `new_email`, `status`, `created_at`, `processed_at`.

Бот пишет/чтёт эти таблицы для регистрации, проверки существования пользователя и управления заявками на смену email; HTTP‑сервис использует `users` и `telegram_users` для проверки `api_key` и получения контактных данных.

---

## Основные потоки данных

### 1. Регистрация пользователя

1. Пользователь отправляет `/register email@example.com` боту.
2. Бот:
   - проверяет, зарегистрирован ли уже этот `telegram_id`;
   - создаёт запись в `users` с email и сгенерированным `api_key`;
   - создаёт запись в `telegram_users`.
3. В дальнейшем для этого Telegram‑аккаунта доступна отправка ссылок.

### 2. Отправка ссылки на файл

1. Пользователь отправляет ссылку боту (или команду `/send <url>`).
2. Бот:
   - убеждается, что пользователь зарегистрирован;
   - сохраняет ссылку в `pendingLinks[telegramID]`;
   - показывает inline‑клавиатуру с режимами доставки.
3. Пользователь выбирает режим.
4. Бот:
   - при необходимости предупреждает про блокировку Gmail для «опасных» расширений и не продолжает;
   - получает `api_key` из БД;
   - вызывает `POST {API_BASE}/send` с `{ api_key, file_url, mode }`.

### 3. Доставка файла

1. HTTP‑сервис по `api_key` находит пользователя и его Telegram‑данные.
2. Выбирается нужная реализация `Delivery` по `mode`.
3. HTTP‑сервис скачивает URL через `internal/downloader`:
   - обычные ссылки — HTTP GET во временный файл;
   - YouTube/Instagram — yt-dlp (для Instagram при заданном `YTDLP_COOKIES_PATH` — с `--cookies`), затем нормализация ffmpeg в MP4 для корректного воспроизведения в Telegram iOS.
4. Дальше реализации delivery используют **путь к локальному файлу**:
   - для email — формируется и отправляется письмо с вложением;
   - для Telegram — файл отправляется в чат через локальный Telegram Bot API (sendVideo/sendDocument).
5. Результат (успех/ошибка) логируется в `/logs/send.log`.

---

## Контейнеризация и окружение

Проект предполагает запуск в Docker Compose:

- контейнер с `bot` (Telegram‑бот);
- контейнер с `http-service` (HTTP‑сервис доставки);
- контейнер с `postgres`;
- контейнер с `telegram-bot-api` (локальный Bot API);
- внешний/внутренний SMTP‑сервер.

Все настройки сервисов передаются через переменные окружения, подробно описанные в `README.md`.

---

## Тесты

Unit-тесты расположены в тех же пакетах, что и код (`*_test.go`):

- **`internal/bot/handlers_test.go`** — тесты для `extractFirstURL` (извлечение первой URL из сообщения по entities).
- **`internal/delivery/multi_test.go`** — тесты для `MultiDelivery.SendFile` (оба канала nil, только email/telegram, ошибки и успех).
- **`internal/httpserver/server_test.go`** — тест для обработчика `GET /health` (статус 200, тело `ok`).

Запуск: `go test ./...` из корня проекта.

