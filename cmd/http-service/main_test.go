// Тесты пакета main http-service: проверка сборки и доступности импортов.
package main

import (
	"testing"

	"download_track/internal/httpserver"
)

// TestPackageBuild проверяет, что пакет собирается и импорт internal/httpserver доступен.
func TestPackageBuild(t *testing.T) {
	// New требует много аргументов; проверяем лишь, что тип Server экспортируется.
	var _ *httpserver.Server
	// Пакет adminnotify, delivery, downloader уже тянутся через main — сборка теста их проверит.
}
