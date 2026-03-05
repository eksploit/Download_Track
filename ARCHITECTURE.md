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
  - ссылка сохраняется в `pendingLinks[telegramID]`;
  - пользователю отправляется inline‑клавиатура с вариантами:
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
  - `TELEGRAM_API_BASE` — базовый URL локального Telegram Bot API (по умолчанию `http://telegram-bot-api:8081`).
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
  - формирует `delivery.User` и вызывает `SendFile(ctx, user, fileURL)`.
  - при ошибках отдаёт соответствующие HTTP‑коды (`401`, `400`, `500`, `502`).

---

## Слой доставки (`internal/delivery`)

### Общие типы

- `type User struct { ID int; Email string; TelegramID int64; Username string; Mode string }`
- `type Delivery interface { SendFile(ctx context.Context, user User, srcURL string) error }`

### Email‑доставка (`EmailDelivery`, `email.go`)

- Отвечает за:
  - скачивание файла по `srcURL` с помощью `http.Client`;
  - сохранение во временный файл;
  - построение MIME‑письма с вложением (через `github.com/scorredoira/email`);
  - отправку письма через SMTP (с поддержкой `STARTTLS`).
- Логирует статусы:
  - `received`, `downloading`, `downloaded`, `send_error`, `sent` с указанием `user_id`, `username`, `mode`, размера файла и т.д.

### Telegram‑доставка (`TelegramDelivery`, `telegram.go`)

- Отвечает за:
  - скачивание файла по `srcURL` во временный файл;
  - отправку этого файла в Telegram‑чат пользователя:
    - `POST {TELEGRAM_API_BASE}/bot{TOKEN}/sendDocument` с `multipart/form-data`;
    - поля: `chat_id`, `document` (бинарное содержимое файла).
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
3. Реализация скачивает файл по указанному URL:
   - в `/tmp/...` или аналогичную директорию;
   - логирует успешное скачивание или ошибку.
4. Дальше:
   - для email — формируется и отправляется письмо с вложением;
   - для Telegram — файл отправляется в чат через локальный Telegram Bot API.
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

