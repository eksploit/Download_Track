package delivery

import "context"

type Mode int

const (
	ModeEmail Mode = iota + 1
	ModeTelegram
	ModeBoth
)

type User struct {
	ID         int
	Email      string
	TelegramID int64
	Username   string
	Mode       string
}

// Delivery доставляет файл пользователю. После введения слоя downloader в src передаётся путь к локальному файлу.
type Delivery interface {
	SendFile(ctx context.Context, user User, src string) error
}

type MultiDelivery struct {
	Email    Delivery
	Telegram Delivery
}
