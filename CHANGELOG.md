### CHANGELOG

### Unreleased

- **`GET /job-log`** (http-service): хвост NDJSON job-лога (`entries`, `truncated`, `parse_errors`), параметр **`lines`**, чтение через **`internal/joblog`**. Регистрация маршрута только при непустых **`ADMIN_JOB_LOG_TOKEN`** и пути **`JOB_LOG_PATH`** (по умолчанию `/logs/send.log` в `cmd/http-service`). Авторизация: **`Authorization: Bearer`** или **`X-Admin-Token`**. Коды **401** / **404** / **503**. Переменные в **`docker-compose.yml`** и **`README.md`**. См. **`ARCHITECTURE.md`** (HTTP-сервис, поток чтения лога).
- **Job-логи `/logs/send.log`**: формат **NDJSON** (`log/slog`, `JSONHandler`) — одна строка, один JSON-объект. В контексте **`POST /send`** — **`request_id`**; событие **`video_pipeline`** (тайминги **`probe_ms`**, **`ytdlp_ms`**, **`ffmpeg_ms`**, размеры, формат); доставка — **`event=delivery`**, **`channel`**, **`stage`**/**`status`**. Обрезка длинных **`url`** — **`logutil.TruncateString`**. Пакеты **`internal/requestid`**, **`internal/logutil`**. См. **`Download_Track.md`**, **`README.md`**, **`ARCHITECTURE.md`**.
- **Пакет `internal/joblog`**: чтение **хвоста** NDJSON-файла без загрузки всего файла в память (**`TailEntries(path, maxLines)`** → **`TailResult`**: **`Entries`** с **`ParsedLine.Fields map[string]any`**, **`ParseErrors`** для битых строк, **`Truncated`** если запрошено больше **`MaxLinesLimit` (100)**). Только стандартная библиотека; юнит-тесты в **`internal/joblog`**. Для выдачи последних событий по HTTP без монтирования логов в контейнер бота см. **`ARCHITECTURE.md`**.
- **Cookies: согласованный расчёт дней и надёжность уведомлений**: добавлена **`DaysLeftCeil(now, expiry)`** в `internal/downloader` — остаток суток до истечения с округлением вверх; **`GET /cookie-status`** и админ-уведомления используют одну логику. **`NotifyAdmin` возвращает `bool`** (успех только при `{"ok":true}`); интерфейс **`httpserver.AdminNotifier`** обновлён. Фоновая проверка срока: **первый прогон сразу при старте**; уведомление об **истёкших** cookies; тексты «Остаётся N …» с **`formatDaysRu`**; флаги отправки и **ретраи** (до 3 попыток, паузы 2 с и 5 с). Документация: `README.md`, `ARCHITECTURE.md`.
- **Уведомление админу о cookies Instagram**: при заданных `TELEGRAM_TOKEN` и `ADMIN_CHAT_ID` http-service при старте проверяет доступность файла cookies (при недоступности или ошибке парсинга шлёт сообщение в Telegram); по таймеру (раз в сутки) уведомляет за 7, 3 и 1 день до истечения; при ошибке загрузки с Instagram из-за логина («login required») — сообщение админу. Добавлен пакет `internal/adminnotify` (Notifier, CheckCookiesFileAtStartup, RunCookieExpiryCheck), в downloader — функция `CookieExpiry(path)` (парсинг Netscape/JSON). Endpoint `GET /cookie-status` возвращает дату истечения, дни до истечения и флаг `expired` (true только если дата уже в прошлом). В боте — админ-команда `/cookie` (показать, сколько дней до истечения cookies; при остатке меньше 1 дня выводится «меньше 1 дня», а не «истекли»).
- **Пайплайн видео**: в **`VideoMeta`** учитываются длительности этапов (мс) для записи в **`video_pipeline`**. Интерфейс `Fetcher` возвращает `FetchResult` с опциональными `VideoMeta`.
- **Instagram: формат загрузки**: для ссылок на Instagram в yt-dlp используется формат `best` (один лучший файл без фильтра по разрешению), так как «видео+аудио» и `best[height<=...]` там часто недоступны («Requested format is not available»).
- **Автотесты**: добавлены тесты для `internal/downloader` (CookieExpiry: пустой/несуществующий файл, Netscape, JSON, неверный формат), `internal/adminnotify` (New, NotifyAdmin, CheckCookiesFileAtStartup, RunCookieExpiryCheck), **`internal/joblog`** (хвост NDJSON, лимиты, большой файл), а также проверка сборки пакетов в `cmd/bot` и `cmd/http-service`. Полный список тестов — в README (раздел «Сборка и автотесты») и в ARCHITECTURE.md (раздел «Тесты»).

### 0.7.0 – Уведомление админу, порядок запуска, логи и имена файлов

- **Уведомление админу о новой регистрации**: при успешной регистрации по `/register` бот отправляет в админ-чат сообщение: @username (или «без username»), telegram_id, email.
- **Docker Compose: порядок запуска бота**: у сервиса `bot` добавлена зависимость от `postgres` с условием `service_healthy`. Устранена гонка при старте и сообщение «lookup postgres: no such host». Зависимости: `postgres` (condition: service_healthy), `http-service` и `telegram-bot-api` (condition: service_started).
- **Логи доставки в Telegram**: в записях доставки добавлено поле `username` наряду с `user_id`, `telegram_id`, `url`, `mode`, `status`.
- **Имя файла при прямой ссылке**: при скачивании по обычной HTTP-ссылке файл сохраняется с именем и расширением из URL; в Telegram и на email документ приходит с тем же именем (раньше — временное `dl-*`).

### 0.6.0 – YouTube и Instagram в чат
- **Ограничение частоты загрузок с Instagram**: минимальный интервал между стартами (`INSTAGRAM_MIN_INTERVAL_SECONDS`) и пауза перед началом в yt-dlp (`YTDLP_SLEEP_INTERVAL_SECONDS`, `--sleep-interval`) для снижения риска rate limit и блокировки аккаунта.
- **Instagram только через cookies**: yt-dlp не поддерживает вход по паролю для Instagram; восстановлена поддержка `YTDLP_COOKIES_PATH` (файл Netscape или JSON с автоконвертацией). Логин/пароль (`INSTAGRAM_USER`/`INSTAGRAM_PASS`) удалены.
- **Сообщение при ошибке загрузки видео**: при неудачной загрузке видео (YouTube/Instagram) пользователю выводится пояснение о возможных ограничениях платформы вместо общего «Ошибка загрузки видео».
- **YouTube и Instagram в чат**: для ссылок на видео (youtube.com, youtu.be, instagram.com) бот сразу запускает скачивание через yt-dlp и доставляет видео в Telegram‑чат без выбора способа доставки. В http-service добавлен слой `internal/downloader` (HTTP GET или yt-dlp), после yt-dlp выполняется нормализация через ffmpeg (MP4/H.264/AAC) для корректного воспроизведения в Telegram iOS. Отправка в Telegram для видео выполняется через `sendVideo` (с `supports_streaming`, `width/height/duration` через ffprobe и `thumb` через ffmpeg). Образ http-service собирается с ffmpeg, yt-dlp и yt-dlp-ejs-rt-deno (Alpine edge). Обычные ссылки по-прежнему обрабатываются с клавиатурой выбора режима.
- **Рефакторинг бота**: пакет `internal/bot` разбит на файлы `bot.go` (жизненный цикл, `Run`), `handlers.go` (обработка сообщений и callback), `repository.go` (доступ к БД), `service.go` (бизнес-логика). Публичный API пакета не изменился.
- **Автотесты**: добавлены unit-тесты для `extractFirstURL` ([internal/bot/handlers_test.go](internal/bot/handlers_test.go)), `MultiDelivery.SendFile` ([internal/delivery/multi_test.go](internal/delivery/multi_test.go)), обработчика `/health` ([internal/httpserver/server_test.go](internal/httpserver/server_test.go)). Запуск: `go test ./...`.
- **Docker Compose**: удалён устаревший атрибут `version` из `docker-compose.yml`.
- **Документация архитектуры**: добавлен подробный файл `ARCHITECTURE.md` с описанием сервисов, слоёв доставки и основных потоков данных.

### 0.5.0 – Фильтр Gmail и безопасность вложений
- **Фильтр Gmail**: добавлен фильтр для адресов `gmail.com`, который блокирует отправку подозрительных/исполняемых файлов (например, `.exe`, `.bat`, `.js`) на почту.
- **Улучшения UX бота**: уточнены сообщения пользователю при блокировке вложений.

### 0.4.0 – Telegram‑доставка файлов и багфиксы
- **Telegram‑доставка**: реализована отправка файлов пользователю в Telegram через локальный Telegram Bot API.
- **Выбор способа доставки**: в боте добавлен выбор режима доставки (`email`, `telegram`, `both`) через inline‑клавиатуру.
- **Фиксы регистрации и отправки**: исправлена повторная регистрация, добавлена предварительная загрузка файла перед отправкой в Telegram.
- **Документация README**: добавлены таблицы переменных окружения, описание локального Telegram Bot API и режимов доставки, примеры HTTP‑запросов и логов.

### 0.3.0 – Разделение на сервисы и слой доставки
- **Разделение на два бинаря**: проект разделён на `bot` (Telegram‑бот) и `http-service` (HTTP‑сервис доставки файлов).
- **Внутренние пакеты**: логика вынесена в `internal/bot`, `internal/httpserver`, `internal/delivery`.
- **Email‑доставка**: добавлена реализация `EmailDelivery` (скачивание файла, формирование письма с вложением, отправка через SMTP).
- **Логирование доставок**: введён лог `/logs/send.log` со статусами этапов доставки (скачивание, отправка, ошибки).

### 0.2.0 – Админский чат и управление email
- **Админский чат**: добавлен отдельный админский чат с командами и меню.
- **Заявки на смену email**: добавлена таблица `email_change_requests` и команды `/change_email`, `/approve_change`, `/reject_change`, `/list_changes`.
- **Улучшения email‑отправки**: внедрена библиотека `github.com/scorredoira/email`, добавлено логирование размеров файлов и статусов, улучшена обработка ошибок.
- **Инфраструктурные изменения**: скрыт внешний порт PostgreSQL, обновлены переменные окружения и шаблон `.env`, вынесен http‑лог из контейнера.

### 0.1.0 – Первая рабочая версия
- **Регистрация пользователей**: реализована команда `/register` с сохранением email и генерацией `api_key`.
- **Привязка Telegram‑аккаунта**: связка `telegram_id` ↔ пользователь в БД.
- **Приём ссылок для скачивания**: бот принимает ссылки на файлы от зарегистрированных пользователей.
- **Уведомления по email**: отправка базового письма‑заглушки о том, что ссылка принята и готова к скачиванию.
