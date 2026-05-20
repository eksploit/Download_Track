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
  - маршруты `/health`, `/send`, `/cookie-status`; при непустых **`ADMIN_JOB_LOG_TOKEN`** и **`jobLogReadPath`** — **`GET /job-log`** (хвост NDJSON через **`internal/joblog.TailEntries`**, авторизация Bearer / **`X-Admin-Token`**);
  - валидация входящего запроса;
  - поиск пользователя по `api_key` в БД;
  - выбор реализации доставки по полю `mode`.

- **`internal/downloader`**  
  Слой скачивания по URL:
  - `Fetch(ctx, url)` возвращает `FetchResult` (путь, `Cleanup`, опционально `VideoMeta` для видео); при видео сервер пишет в jobLog JSON-событие **`video_pipeline`** (url, оценка по пробе, формат, размеры, `probe_ms` / `ytdlp_ms` / `ffmpeg_ms`);
  - для обычных URL — HTTP GET во временный файл;
  - для YouTube/Instagram — yt-dlp (на загрузку одного файла отводится до 20 минут; если за это время загрузка не завершилась, она отменяется), затем нормализация ffmpeg в MP4 для корректного воспроизведения в Telegram iOS.
  - **Ограничение разрешения по размеру**: перед скачиванием вызывается yt-dlp `--dump-single-json` (без загрузки); по списку форматов оценивается размер варианта «лучшее видео до 1080p + лучшее аудио». Если суммарный размер ≤100 МБ — используется формат до 1080p, иначе до 720p (`chooseFormatBySize`). Так снижается нагрузка на ffmpeg и риск таймаута на слабом CPU. Для **Instagram** в yt-dlp передаётся формат `best` (без фильтра по разрешению), так как раздельные потоки и фильтры по height там часто недоступны («Requested format is not available»).
  - **Нагрузка на CPU**: перекодирование тяжёлого видео (4K, 60 fps, AV1) на одном ядре может идти в 5–10 раз медленнее реального времени (например, speed=0.16× и ~28 минут на ролик 4:39). Для более высокого качества без таймаутов нужен более мощный процессор или увеличение таймаута.
  - Опционально для Instagram: путь к файлу cookies (`CookiesPath`, задаётся через `YTDLP_COOKIES_PATH`). Для ссылок на Instagram yt-dlp вызывается с `--cookies`; поддерживаются форматы Netscape и JSON (конвертация в `cookiesPathForYtDlp`). Функция **`CookieExpiry(path)`** возвращает минимальную дату истечения по файлу cookies (Netscape или JSON); используется для проверки срока и уведомлений админу. Функция **`DaysLeftCeil(now, expiry)`** — число суток до момента `expiry` относительно `now`: длительность до истечения делится на 24 часа и округляется **вверх** (`ceil`); если срок уже наступил или прошёл — `0`. Используется в `GET /cookie-status` и в `internal/adminnotify` для согласованности с текстами уведомлений.
  - Минимальный интервал между стартами загрузок (`InstagramMinInterval`) и пауза `--sleep-interval` (`YtDlpSleepInterval`) снижают риск rate limit.

- **`internal/adminnotify`**  
  Уведомления администратору в Telegram (cookies, ошибки Instagram):
  - **`Notifier`** — отправка текстового сообщения в чат админа через Bot API (POST sendMessage). Создаётся через `New(token, apiBase, adminChatID)`; при пустом token или adminChatID возвращается nil. Метод **`NotifyAdmin(msg string) bool`** возвращает `true` только при HTTP 200 и теле `{"ok":true}`; ошибки пишутся в стандартный лог.
  - **`CheckCookiesFileAtStartup(cookiesPath, notifier)`** — при старте http-service проверяет доступность файла cookies; при недоступности или ошибке парсинга отправляет админу сообщение (с теми же повторами, что и фоновый цикл).
  - **`RunCookieExpiryCheck(cookiesPath, notifier, interval)`** — фоновый цикл: **первая итерация выполняется сразу** после запуска, затем с заданным интервалом (в `cmd/http-service` — 24 ч). Читает `CookieExpiry` и **`DaysLeftCeil`**: при **истёкших** cookies — одно сообщение на дату минимального expiry; при остатке **≤7 / ≤3 / ≤1** суток — напоминания с **фактическим** `N` в тексте и склонением (`formatDaysRu`). Флаги «уже отправлено» для порогов и для истечения выставляются **только после успешного** `NotifyAdmin`; при неудаче — до трёх попыток с паузами 2 с и 5 с (`notifyWithRetry`). Интерфейс **`httpserver.AdminNotifier`** объявляет `NotifyAdmin(msg string) bool` (для ошибки Instagram возвращаемое значение может игнорироваться).

- **`internal/delivery`**  
  Слой доставки файлов:
  - `delivery.go` — интерфейс `Delivery` и тип `User`;
  - `email.go` — реализация `EmailDelivery` через SMTP;
  - `telegram.go` — реализация `TelegramDelivery` через локальный Telegram Bot API;
  - `multi.go` — `MultiDelivery`, агрегирующая email и Telegram‑доставку.

- **`internal/joblog`**  
  Разбор **NDJSON** job-лога (путь задаётся **`JOB_LOG_PATH`**, по умолчанию как **`/logs/send.log`**): **`TailEntries(path, maxLines)`** возвращает **`TailResult`** с последними непустыми строками, каждая успешно распарсенная — **`ParsedLine`** с **`Fields map[string]any`**; **`ParseErrors`** — число строк с невалидным JSON; **`Truncated`** — если **`maxLines`** превышал **`MaxLinesLimit` (100)** (фактически читается не больше лимита). Чтение с **конца файла** ограниченным чанком; при чтении не с начала файла первая строка в чанке отбрасывается как потенциально обрезанная. Зависимости — только стандартная библиотека. В **http-service** используется обработчиком **`GET /job-log`**, чтобы отдавать последние события по сети без монтирования каталога логов в контейнер бота (секрет **`ADMIN_JOB_LOG_TOKEN`**).

---

## Telegram‑бот (`cmd/bot`, `internal/bot`)

### Точка входа `cmd/bot/main.go`

- Читает переменные окружения:
  - `TELEGRAM_TOKEN` — токен бота;
  - `API_BASE` — базовый URL HTTP‑сервиса (по умолчанию `http://http-service:8080`);
  - `DB_DSN` — строка подключения к PostgreSQL;
  - `ADMIN_CHAT_ID` — Telegram‑ID администратора;
  - **`ADMIN_JOB_LOG_TOKEN`** (опционально) — тот же секрет, что у `http-service`, для вызова **`GET /job-log`** из бота; если пусто, админ-команда **`/logs`** сообщает, что токен не настроен.
- В **`docker-compose.yml`** в контейнер `bot` прокидывается **`ADMIN_JOB_LOG_TOKEN`** из общего `.env`. Просмотр хвоста лога с хоста без бота: **`curl`** с Bearer и токеном из `.env` (см. **`README.md`**, **`Download_Track.md`**).
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
  - `/approve_change <id>`, `/reject_change <id>`, `/list_changes` — админские операции через сервис и репозиторий (`email_change_requests`);
  - `/cookie` — только в админ-чате: запрос `GET {API_BASE}/cookie-status` к http-service и ответ админу (сколько дней до истечения cookies Instagram или сообщение о недоступности/ошибке формата).
  - **`/logs [N]`** — только в админ-чате: при непустом **`ADMIN_JOB_LOG_TOKEN`** — **`svc.GetJobLogPreview`**: `GET {API_BASE}/job-log?lines=N` с заголовком **`Authorization: Bearer`**, разбор JSON, форматирование в текст (группы по **`request_id`**, компактные поля для **`video_pipeline`** / **`delivery`**). Для привязки к пользователю в конец строки добавляется **`jobLogUserSuffix`** (если в объекте есть соответствующие поля): **`uid=`** из **`user_id`**, **`@username`** из **`username`**, **`tg=`** из **`telegram_id`**; для событий без этих полей суффикс пустой. **`N`** по умолчанию 20, не больше **`internal/joblog.MaxLinesLimit` (100)**. Если текст длиннее **4096** символов (по рунам) — отправка **документа** `job-log.txt` вместо сообщения.

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
  - `TELEGRAM_TOKEN` — токен Telegram‑бота для доставки и (вместе с `ADMIN_CHAT_ID`) для уведомлений админу;
  - `TELEGRAM_API_BASE` — базовый URL локального Telegram Bot API (по умолчанию `http://telegram-bot-api:8081`);
  - `ADMIN_CHAT_ID` — при заданном вместе с `TELEGRAM_TOKEN` включаются уведомления админу о cookies и ошибках Instagram;
  - `YTDLP_COOKIES_PATH` — необязательный путь к файлу cookies для Instagram (внутри контейнера); используется также для проверки срока и endpoint `/cookie-status`;
  - `JOB_LOG_PATH` — путь к NDJSON job-логу (по умолчанию `/logs/send.log`); тот же путь передаётся в `httpserver` для **`GET /job-log`**;
  - `ADMIN_JOB_LOG_TOKEN` — если непустой, регистрируется **`GET /job-log`** с проверкой Bearer / `X-Admin-Token`; если пустой — маршрут не регистрируется;
  - `INSTAGRAM_MIN_INTERVAL_SECONDS`, `YTDLP_SLEEP_INTERVAL_SECONDS` — ограничение частоты загрузок с Instagram (интервал между стартами и пауза перед началом).
- Инициализирует:
  - подключение к БД;
  - `*slog.Logger` с JSON handler на файл по **`JOB_LOG_PATH`** (NDJSON);
  - `EmailDelivery` с конфигурацией SMTP;
  - `TelegramDelivery` (если задан `TELEGRAM_TOKEN`);
  - `MultiDelivery` для режима `both`;
  - при наличии `TELEGRAM_TOKEN` и `ADMIN_CHAT_ID` — `adminnotify.Notifier`; при заданном `YTDLP_COOKIES_PATH` вызывает `CheckCookiesFileAtStartup` и запускает горутину `RunCookieExpiryCheck` (интервал 24 ч; **первый прогон проверки срока — сразу при старте**).
- Создаёт `httpserver.Server` (с опциональным `AdminNotifier`, путём к cookies, путём к job-логу и токеном **`ADMIN_JOB_LOG_TOKEN`**), регистрирует маршруты и запускает `http.Server` на `:8080`.

### HTTP‑слой (`internal/httpserver/server.go`)

`Server` содержит:

- `db *sql.DB` — доступ к БД;
- `jobLog *slog.Logger` — job-лог доставки (NDJSON в `/logs/send.log`);
- `fetcher downloader.Fetcher` — слой скачивания (обычный HTTP или yt-dlp);
- `emailDelivery`, `telegramDelivery`, `bothDelivery` — реализации `Delivery`;
- `adminNotifier AdminNotifier` (опционально) — уведомление админу в Telegram; при ошибке `Fetch` для URL с instagram.com и тексте ошибки «login» вызывается `NotifyAdmin` (возвращает `bool`, успех не обязателен для обработчика);
- `cookiesPath` — путь к файлу cookies (для `GET /cookie-status`);
- `jobLogReadPath`, `adminJobLogToken` — при **обоих** непустых регистрируется **`GET /job-log`**: хвост файла job-лога через `joblog.TailEntries`, JSON с полями `entries`, `truncated`, `parse_errors`; **401** при отсутствии/неверном токене (**`Authorization: Bearer`** или **`X-Admin-Token`**); **404** если файл не найден; **503** при иной ошибке чтения.

Роуты:

- `GET /health` — простая проверка живости.
- `GET /cookie-status` — статус cookies Instagram: при успешном парсинге — JSON с датой минимального истечения (`expiry`), **`days_left` по `DaysLeftCeil`** (согласовано с админ-уведомлениями), флаг `expired` если срок уже прошёл; при недоступном файле или ошибке парсинга — соответствующий признак/сообщение. Используется ботом для команды `/cookie`.
- `GET /job-log` — (опционально, см. выше) последние строки NDJSON job-лога; query **`lines`** (по умолчанию 20).
- `POST /send` — основной эндпоинт:
  - декодирует JSON `sendRequest { api_key, file_url, mode }`;
  - по `api_key` находит пользователя, его Telegram‑account и email;
  - по полю `mode` выбирает реализацию:
    - `email` → `emailDelivery`;
    - `telegram` → `telegramDelivery`;
    - `both` → `bothDelivery`.
  - для `POST /send` кладёт в контекст **`request_id`** (`internal/requestid`) и передаёт `ctx` в `fetcher.Fetch` и `SendFile`;
  - скачивает URL через `fetcher.Fetch(ctx, file_url)` → `result` (Path, Cleanup, VideoMeta); при `result.VideoMeta != nil` логирует в jobLog JSON-событие **`video_pipeline`** (в т.ч. `probe_ms`, `ytdlp_ms`, `ffmpeg_ms`, размеры и формат);
  - `defer result.Cleanup()` удаляет временный файл после доставки;
  - формирует `delivery.User` и вызывает `SendFile(ctx, user, result.Path)`.
  - при ошибках отдаёт соответствующие HTTP‑коды (`401`, `400`, `500`, `502`).

---

## Слой доставки (`internal/delivery`)

### Общие типы

- `type User struct { ID int; Email string; TelegramID int64; Username string; Mode string }`
- `type Delivery interface { SendFile(ctx context.Context, user User, src string) error }` — `src` это путь к локальному файлу (`result.Path` после `downloader.Fetch`).

### Email‑доставка (`EmailDelivery`, `email.go`)

- Отвечает за:
  - чтение вложения из локального файла по пути `src`;
  - построение MIME‑письма с вложением (через `github.com/scorredoira/email`);
  - отправку письма через SMTP (с поддержкой `STARTTLS`).
- Логирует в job-лог (slog JSON) события `delivery` / `channel=email` со стадиями вроде `received`, `downloading`, `downloaded`, `send_error`, `sent`; поля включают `request_id` из контекста, `user_id`, `username`, `mode`, размер и т.д.

### Telegram‑доставка (`TelegramDelivery`, `telegram.go`)

- Отвечает за:
  - отправку локального файла в Telegram‑чат пользователя через локальный Telegram Bot API:
    - для видео — `sendVideo` (встроенный плеер в чате), иначе `sendDocument`;
    - дополнительно выставляются `supports_streaming`, `width/height/duration` (через ffprobe) и `thumb` (JPEG‑превью через ffmpeg) для лучшей совместимости iOS.
- Использует локальный Telegram Bot API контейнер, который уже инкапсулирует MTProto‑взаимодействие с Telegram.
- Логирует события `delivery` / `channel=telegram` со стадиями `request`, `download_error`, `downloaded`, `api_error`, `sent` и др.

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
5. Результат (успех/ошибка) логируется в `/logs/send.log` (NDJSON; см. `README.md`, `Download_Track.md`).

---

## Контейнеризация и окружение

Проект предполагает запуск в Docker Compose:

- контейнер с `bot` (Telegram‑бот);
- контейнер с `http-service` (HTTP‑сервис доставки);
- контейнер с `postgres`;
- контейнер с `telegram-bot-api` (локальный Bot API);
- внешний/внутренний SMTP‑сервер.

Все настройки сервисов передаются через переменные окружения, подробно описанные в `README.md`. В `docker-compose.yml` в окружение `http-service` прокидываются те же `TELEGRAM_TOKEN`, `TELEGRAM_API_BASE` и `ADMIN_CHAT_ID`, что и у `bot` (значения из общего `.env`); также **`JOB_LOG_PATH`** (по умолчанию `/logs/send.log`) и **`ADMIN_JOB_LOG_TOKEN`**. У **`bot`** токен **`ADMIN_JOB_LOG_TOKEN`** передаётся из `.env` и читается в **`cmd/bot/main.go`** для **`/logs`**. Шаблон переменных — **`.env.example`**, порядок восстановления — **`ENV_RESTORE_GUIDE.md`**.

### Поток: хвост job-лога по HTTP

Клиент с секретом (**админ или бот** в Docker-сети) вызывает **`GET http-service:8080/job-log?lines=N`** с заголовком **`Authorization: Bearer`** или **`X-Admin-Token`**. **http-service** не открывает каталог логов наружу как статику: он читает файл по **`JOB_LOG_PATH`** через **`internal/joblog`**, возвращает JSON. Контейнер **bot** к **`./http-logs` не монтируется** — доступ к логам только через этот API (при настроенном токене).

---

## Тесты

Unit-тесты расположены в тех же пакетах, что и код (`*_test.go`):

- **`internal/bot/handlers_test.go`** — тесты для `extractFirstURL` (извлечение первой URL из сообщения по entities).
- **`internal/delivery/multi_test.go`** — тесты для `MultiDelivery.SendFile` (оба канала nil, только email/telegram, ошибки и успех).
- **`internal/httpserver/server_test.go`** — тесты для `GET /health` (статус 200, тело `ok`); **`GET /job-log`** (маршрут не регистрируется без токена; 401 без заголовка; 404 при отсутствии файла лога; 200 с Bearer и с `X-Admin-Token`).
- **`internal/downloader/downloader_test.go`** — тесты для `CookieExpiry`: несуществующий файл, пустой файл, только пробелы, формат Netscape (минимальная дата, только комментарии), формат JSON (минимальная дата, пустой массив, отсутствие дат истечения), неверный формат; тесты для **`DaysLeftCeil`** (истёкший срок, границы округления).
- **`internal/adminnotify/adminnotify_test.go`** — тесты для `New` (nil при пустом token или adminChatID, подстановка apiBase по умолчанию), **`NotifyAdmin` (bool; POST на мок-сервер; ответы `ok:false` и не-200)**, `formatDaysRu`, `CheckCookiesFileAtStartup`, **`runCookieExpiryIteration` (ретраи, флаг только после успеха)**, `RunCookieExpiryCheck` (при пустом пути горутина сразу завершается).
- **`internal/joblog/joblog_test.go`** — тесты для **`TailEntries`**: пустой файл; NDJSON с битой строкой; усечение **`maxLines`** сверх **`MaxLinesLimit`**; большой файл (последние записи); файл больше внутреннего чанка чтения (хвост и маркер в конце); **`maxLines` = 0**.
- **`cmd/bot/main_test.go`** — проверка сборки пакета и доступности импорта `internal/bot` (символ `ErrAlreadyRegistered`).
- **`cmd/http-service/main_test.go`** — проверка сборки пакета и доступности импорта `internal/httpserver` (тип `Server`).

Запуск: `go test ./...` из корня проекта.

