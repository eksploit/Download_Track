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
}

type Delivery interface {
    SendFile(ctx context.Context, user User, srcURL string) error
}
