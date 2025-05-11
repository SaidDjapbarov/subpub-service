// Простая in‑memory шина «Publisher / Subscriber»
//
// У одного subject может быть много подписчиков.
// Медленный подписчик не замедляет остальных.
// Для каждого подписчика порядок сообщений сохраняется (FIFO).
// Close(ctx) останавливает публикации; ждёт, пока обработчики
// доработают, или выходит сразу, если переданный контекст отменён.
//
// Каждый подписчик держит собственную буферизированную очередь
// (канал) + одну горутину, которая последовательно вызывает
// пользовательский колбэк.

package subpub

import (
	"context"
	"errors"
	"sync"
)

// ------------------------- Публичные типы -------------------------

// MessageHandler — функция, которую пользователь передаёт при
// подписке; она вызывается для каждого доставленного сообщения.
// msg имеет тип interface{}, поэтому вызывающая сторона сама
// приводит его к нужному конкретному типу.
type MessageHandler func(msg interface{})

// Subscription позволяет отписаться от конкретного subject.
type Subscription interface {
	Unsubscribe()
}

// SubPub — основной интерфейс шины.
type SubPub interface {
	Subscribe(subject string, cb MessageHandler) (Subscription, error)
	Publish(subject string, msg interface{}) error
	Close(ctx context.Context) error
}

// ErrClosed возвращается, если попытаться опубликовать или
// подписаться после закрытия шины.
var ErrClosed = errors.New("subpub: шина закрыта")

// NewSubPub создаёт новую шину.
func NewSubPub() SubPub {
	return &subPub{
		subs: make(map[string][]*subscription),
	}
}

// ------------------------- Внутренние типы ------------------------

// subPub представляет собой шину, хранит карту subject → список подписок.
// Все записи защищены RW‑mutex, читать одновременно могут все, кто хочет.
// wg используется, чтобы дожидаться завершения всех горутин при Close.
type subPub struct {
	mu     sync.RWMutex
	subs   map[string][]*subscription
	closed bool
	wg     sync.WaitGroup
}

// subscription представляет собой подписчика, инкапсулирует очередь и
// worker() конкретного подписчика.
type subscription struct {
	parent  *subPub          // ссылка на шину, она нужна для удаления из map
	subject string           // какой subject слушаем
	ch      chan interface{} // буферизированный FIFO-канал
	cb      MessageHandler   // пользовательский обработчик

	once   sync.Once  // чтобы больше одного раза Unsubscribe не вызывался
	sendMu sync.Mutex // сохраняет порядок отправки сообщений
}

// --------------------------- Subscribe ----------------------------

func (sp *subPub) Subscribe(subject string, cb MessageHandler) (Subscription, error) {
	sp.mu.Lock()
	defer sp.mu.Unlock()

	// Если шина закрыта, то подписаться не можем.
	if sp.closed {
		return nil, ErrClosed
	}

	// Создаём подписку с небольшим буфером.
	sub := &subscription{
		parent:  sp,
		subject: subject,
		ch:      make(chan interface{}, 64),
		cb:      cb,
	}

	// Записываем в отображение нового подписчика.
	sp.subs[subject] = append(sp.subs[subject], sub)

	// Запускаем единственную горутину‑worker, которая читает из
	// очереди и последовательно вызывает колбэк.
	sp.wg.Add(1)
	go sub.worker()

	return sub, nil
}

// worker — горутина каждой подписки, которая слушает канал и
// вызывает колбэки для каждого полученного сообщения.
func (s *subscription) worker() {
	defer s.parent.wg.Done()
	for msg := range s.ch {
		s.cb(msg)
	}
}

// ---------------------------- Publish ----------------------------

func (sp *subPub) Publish(subject string, msg interface{}) error {
	// Проверка, не закрыта ли шина.
	sp.mu.RLock()
	if sp.closed {
		sp.mu.RUnlock()
		return ErrClosed
	}
	// Делаем копию среза подписок, чтобы не держать RLock во время публикации,
	// ведь публикация может быть длительной, а другие методы в этот момент
	// могут хотеть захватить Lock.
	var empty []*subscription
	subsCopy := append(empty, sp.subs[subject]...)
	sp.mu.RUnlock()

	// Рассылаем сообщение каждому подписчику.
	for _, sub := range subsCopy {
		sub.enqueue(msg)
	}
	return nil
}

// enqueue кладёт сообщение в очередь подписчика, сохраняя порядок,
// даже если его буфер заполнен.
//
// Идея: sendMu гарантирует, что одновременно тот, кто публикует в канал,
// может быть только один. Если буфер не заполнен — отправляем сразу и
// отпускаем sendMu. Если заполнен — создаём отдельную горутину, которая
// заблокируется на отправке, а мы выходим, не мешая другим подписчикам.
// Второй enqueue не пройдёт sendMu, пока предыдущий не закончит, поэтому
// порядок сообщений остаётся FIFO.
func (s *subscription) enqueue(msg interface{}) {
	s.sendMu.Lock()

	select {
	case s.ch <- msg:
		// Буфер был свободен — сразу отправили.
		s.sendMu.Unlock()
	default:
		// Буфер полный: отправляем в отдельной горутине и держим sendMu,
		// пока сообщение не добавим в канал.
		go func() {
			defer s.sendMu.Unlock()
			// Если канал к этому моменту закрыт, recover подавит панику.
			defer func() { _ = recover() }()
			s.ch <- msg
		}()
	}
}

// -------------------------- Unsubscribe --------------------------

func (s *subscription) Unsubscribe() { s.unsubscribe() }

func (s *subscription) unsubscribe() {
	s.once.Do(func() {
		// 1. Удаляем себя из отображения.
		sp := s.parent
		sp.mu.Lock()
		if list, ok := sp.subs[s.subject]; ok {
			for i, v := range list {
				if v == s {
					list[i] = list[len(list)-1]
					list = list[:len(list)-1]
					break
				}
			}
			if len(list) == 0 {
				delete(sp.subs, s.subject)
			} else {
				sp.subs[s.subject] = list
			}
		}
		sp.mu.Unlock()

		// 2. Закрываем канал — это сигнал worker завершиться.
		close(s.ch)
	})
}

// ----------------------------- Close -----------------------------

func (sp *subPub) Close(ctx context.Context) error {
	// Блокируем доступ к шине, чтобы никто не успел добавить подписчиков.
	sp.mu.Lock()

	// Закрываем шину.
	if sp.closed {
		sp.mu.Unlock()
		return ErrClosed
	}
	sp.closed = true

	// Собираем все подписки в список, чтобы закрыть их каналы позже.
	var toClose []*subscription
	for _, list := range sp.subs {
		toClose = append(toClose, list...)
	}
	// Очищаем карту: новые Publish уже невозможны, т.к. closed=true.
	sp.subs = nil
	sp.mu.Unlock()

	// Закрываем каналы всех подписчиков, чтобы их воркеры завершились.
	for _, sub := range toClose {
		sub.unsubscribe()
	}

	// Ждём завершения всех воркеров или отмену контекста.
	done := make(chan struct{})
	go func() {
		sp.wg.Wait()
		close(done)
	}()

	// Выбираем: либо воркеры закончили, либо контекст отменён.
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
