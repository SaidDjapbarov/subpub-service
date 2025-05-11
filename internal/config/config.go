// Пакет config отвечает за загрузку и хранение настроек сервиса из YAML-файла.
// Конфигурация включает:
//  1. GRPCPort        — адрес/порт для запуска gRPC-сервера
//  2. ShutdownTimeout — время ожидания graceful shutdown
//  3. LogLevel        — уровень логирования ("debug", "info", "warn", "error")

package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Config содержит все настройки сервиса.
type Config struct {
	GRPCPort        string        `yaml:"grpc_port"`
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout"`
	LogLevel        string        `yaml:"log_level"`
}

// MustLoad читает YAML‑файл и паникует при ошибке.
// При любой ошибки паникуем, чтобы не делать много проверок.
func MustLoad(path string) *Config {
	data, err := os.ReadFile(path)
	if err != nil {
		panic("не удалось прочитать конфиг: " + err.Error())
	}

	var c Config
	// Разбираем YAML в структуру Config.
	if err := yaml.Unmarshal(data, &c); err != nil {
		panic("не удалось распарсить конфиг: " + err.Error())
	}

	// Если что-то не задали, ставим по умлочанию поля.
	if c.GRPCPort == "" {
		c.GRPCPort = ":50051"
	}
	if c.ShutdownTimeout == 0 {
		c.ShutdownTimeout = 5 * time.Second
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}

	return &c
}
