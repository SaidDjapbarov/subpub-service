// cmd/server/main.go
//
// Точка входа в сервис.
// Здесь:
//   1. Загружаем конфигурацию из файла config.yaml.
//   2. Настраиваем логирование.
//   3. Создаём шину событий (из пакета subpub).
//   4. Поднимаем gRPC-сервер с методами Publish и Subscribe.
//   5. Включаем gRPC Reflection (для grpcurl и отладки).
//   6. Ловим SIGINT/SIGTERM и выполняем graceful shutdown:
//      - останавливаем приём новых RPC,
//      - дожидаемся отправки всех сообщений в шине.

package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/SaidDjapbarov/subpub-service/internal/config"
	"github.com/SaidDjapbarov/subpub-service/internal/logger"
	"github.com/SaidDjapbarov/subpub-service/subpub"

	"github.com/SaidDjapbarov/subpub-service/internal/app"
	pb "github.com/SaidDjapbarov/subpub-service/proto"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	// Читаем конфиг и поднимаем логер.
	cfg := config.MustLoad("config.yaml")
	log := logger.New(cfg.LogLevel)

	// Создаем шину.
	bus := subpub.NewSubPub()

	// Инициализируем gRPC сервер и регистрируем сервис PubSub.
	grpcSrv := grpc.NewServer()
	pb.RegisterPubSubServer(grpcSrv, app.NewServer(bus, log))

	// Слушаем TCP‑порт из конфига.
	lis, err := net.Listen("tcp", cfg.GRPCPort)
	if err != nil {
		log.Error("не удалось слушать порт", "port", cfg.GRPCPort, "err", err)
		os.Exit(1)
	}

	// Для удобства работы с grpcurl
	reflection.Register(grpcSrv)

	// Запускаем наш сервер в фоновом потоке.
	go func() {
		log.Info("gRPC сервер запущен", "addr", cfg.GRPCPort)
		if err := grpcSrv.Serve(lis); err != nil {
			log.Error("gRPC сервер остановлен с ошибкой", "err", err)
		}
	}()

	// Ловим SIGINT / SIGTERM.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop // до самого сигнала спим

	log.Info("получен сигнал завершения")

	// Останавливаем прием новых RPC и дожидаемся завершения текущих.
	go grpcSrv.GracefulStop()

	// Чтоб шина дочитала все сообщения.
	ctx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()

	if err := bus.Close(ctx); err != nil {
		// Если контекст истёк — логируем, но всё равно выходим
		log.Error("закрытие шины прервано по таймауту", "err", err)
	}

	log.Info("сервис корректно остановлен")
}
