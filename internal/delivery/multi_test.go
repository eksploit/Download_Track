package delivery

import (
	"context"
	"errors"
	"testing"
)

// fakeDelivery — заглушка Delivery для тестов; возвращает заданную ошибку.
type fakeDelivery struct {
	err error
}

func (f *fakeDelivery) SendFile(ctx context.Context, user User, srcURL string) error {
	return f.err
}

func TestMultiDelivery_SendFile(t *testing.T) {
	errEmail := errors.New("email error")
	errTelegram := errors.New("telegram error")

	t.Run("both nil", func(t *testing.T) {
		m := &MultiDelivery{Email: nil, Telegram: nil}
		err := m.SendFile(context.Background(), User{}, "https://example.com/file")
		if err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err.Error() != "no delivery configured" {
			t.Errorf("got %q", err.Error())
		}
	})

	t.Run("only email, success", func(t *testing.T) {
		m := &MultiDelivery{Email: &fakeDelivery{err: nil}, Telegram: nil}
		err := m.SendFile(context.Background(), User{}, "https://example.com/file")
		if err != nil {
			t.Errorf("ожидался nil, got %v", err)
		}
	})

	t.Run("only telegram, success", func(t *testing.T) {
		m := &MultiDelivery{Email: nil, Telegram: &fakeDelivery{err: nil}}
		err := m.SendFile(context.Background(), User{}, "https://example.com/file")
		if err != nil {
			t.Errorf("ожидался nil, got %v", err)
		}
	})

	t.Run("both set, email error", func(t *testing.T) {
		m := &MultiDelivery{
			Email:    &fakeDelivery{err: errEmail},
			Telegram: &fakeDelivery{err: nil},
		}
		err := m.SendFile(context.Background(), User{}, "https://example.com/file")
		if err != errEmail {
			t.Errorf("ожидался errEmail, got %v", err)
		}
	})

	t.Run("both set, telegram error", func(t *testing.T) {
		m := &MultiDelivery{
			Email:    &fakeDelivery{err: nil},
			Telegram: &fakeDelivery{err: errTelegram},
		}
		err := m.SendFile(context.Background(), User{}, "https://example.com/file")
		if err != errTelegram {
			t.Errorf("ожидался errTelegram, got %v", err)
		}
	})

	t.Run("both set, both error", func(t *testing.T) {
		m := &MultiDelivery{
			Email:    &fakeDelivery{err: errEmail},
			Telegram: &fakeDelivery{err: errTelegram},
		}
		err := m.SendFile(context.Background(), User{}, "https://example.com/file")
		if err == nil {
			t.Fatal("ожидалась ошибка")
		}
		if err.Error() != "email error: email error; telegram error: telegram error" {
			t.Errorf("got %q", err.Error())
		}
	})

	t.Run("both set, success", func(t *testing.T) {
		m := &MultiDelivery{
			Email:    &fakeDelivery{err: nil},
			Telegram: &fakeDelivery{err: nil},
		}
		err := m.SendFile(context.Background(), User{}, "https://example.com/file")
		if err != nil {
			t.Errorf("ожидался nil, got %v", err)
		}
	})
}
