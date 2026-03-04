package delivery

import (
    "context"
    "fmt"
)

func (m *MultiDelivery) SendFile(ctx context.Context, user User, srcURL string) error {
    if m.Email == nil && m.Telegram == nil {
        return fmt.Errorf("no delivery configured")
    }

    var emailErr, telegramErr error

    if m.Email != nil {
        emailErr = m.Email.SendFile(ctx, user, srcURL)
    }

    if m.Telegram != nil {
        telegramErr = m.Telegram.SendFile(ctx, user, srcURL)
    }

    if emailErr != nil && telegramErr != nil {
        return fmt.Errorf("email error: %v; telegram error: %v", emailErr, telegramErr)
    }
    if emailErr != nil {
        return emailErr
    }
    if telegramErr != nil {
        return telegramErr
    }

    return nil
}
