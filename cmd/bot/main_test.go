// Тесты пакета main бота: проверка сборки и доступности импортов.
package main

import (
	"testing"

	"download_track/internal/bot"
)

// TestPackageBuild проверяет, что пакет собирается и импорт internal/bot доступен.
func TestPackageBuild(t *testing.T) {
	_ = bot.ErrAlreadyRegistered
}
