// Пакет logger создаёт простой логгер на базе стандартного log/slog.
// Позволяет указать уровень логирования через конфиг (debug, info, warn, error).
//
// Пример использования:
//   log := logger.New("debug")
//   log.Info("сервис запущен", "port", ":50051")

package logger

import (
	"log/slog"
	"os"
	"strings"
)

// New возвращает настроенный slog.Logger.
// levelStr ожидает одну из строк: "debug", "info", "warn", "error".
// В случае неизвестного значения будет уровень "info".
func New(levelStr string) *slog.Logger {
	var lvl slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
	})
	return slog.New(handler)
}
