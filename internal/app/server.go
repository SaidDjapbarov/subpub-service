// Здесь реализован gRPC-сервис PubSub поверх шины subpub.
// Методы:
//   - Publish: принимает ключ и данные и публикует их в шину;
//   - Subscribe: открывает стрим, получает из шины события по ключу и передаёт их клиенту.
//
// Зависимости (constructor injection):
//   bus — шина subpub.SubPub
//   log — логер на базе slog

package app

import (
	"context"
	"log/slog"

	pb "github.com/SaidDjapbarov/subpub-service/proto"
	"github.com/SaidDjapbarov/subpub-service/subpub"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Server реализует gRPC‑интерфейс PubSub поверх нашей шины subpub.
// Встраиваем UnimplementedPubSubServer, чтобы не писать весь интерфейс вручную.
type Server struct {
	pb.UnimplementedPubSubServer               // для обратной совместимости
	bus                          subpub.SubPub // шина
	log                          *slog.Logger  // логер для событий сервиса
}

// NewServer создает новый экземпляр сервера с зависимостями.
func NewServer(bus subpub.SubPub, log *slog.Logger) *Server {
	return &Server{
		bus: bus,
		log: log,
	}
}

// Publish – обрабатывает unary-запрос для побуликации события.
// Если шина закрыта, возвращает codes.Unavailable.
func (s *Server) Publish(ctx context.Context, req *pb.PublishRequest) (*emptypb.Empty, error) {
	// Пытаемся опубликовать в шину
	if err := s.bus.Publish(req.GetKey(), req.GetData()); err != nil {
		// Клиент получит ошибку сетевого уровня
		return nil, status.Error(codes.Unavailable, err.Error())
	}
	// Логируем только в режиме debug
	s.log.Debug("publish",
		"key", req.GetKey(),
		"data", req.GetData(),
	)
	return &emptypb.Empty{}, nil
}

// Subscribe – обрабатывает потоковый запрос: подписывается на ключ
// и пробрасывает все пришедшие сообщения клиенту.
func (s *Server) Subscribe(req *pb.SubscribeRequest, stream pb.PubSub_SubscribeServer) error {
	// Регистрируем callback, который шлёт сообщение в gRPC-поток.
	sub, err := s.bus.Subscribe(req.GetKey(), func(msg interface{}) {
		// msg гарантированно имеет тип string.
		_ = stream.Send(&pb.Event{Data: msg.(string)})
	})
	if err != nil {
		// Если шина закрыта.
		return status.Error(codes.Unavailable, err.Error())
	}
	defer sub.Unsubscribe()

	// Ждём отмены со стороны клиента или остановки сервера.
	<-stream.Context().Done()
	return nil
}
