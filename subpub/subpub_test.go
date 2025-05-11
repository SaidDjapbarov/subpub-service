// Unit-тесты для пакета subpub.
//
// В тестах проверяется:
//  1. Порядок доставки (FIFO) для каждого подписчика.
//  2. Медленные подписчики не замедляют остольных
//  3. Корректная отписка: после Unsubscribe новые сообщения не приходят.
//  4. Поведение Close(ctx): дожидается доставки всех опубликованных сообщений.
//  5. Поведение Close(ctx) при отменённом контексте: метод возвращает ошибку и
//     не блокирует вызывающий код.
//  6. Отсутствие утечек горутин после подписки, отписки и закрытия шины.
//
// Запуск:
// go test ./subpub

package subpub

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// TestFIFO проверяет, что один подписчик получает все сообщения
// в том порядке, в каком они публиковались.
func TestFIFO(t *testing.T) {
	// Создаём новую шину и отложенно закрываем её.
	bus := NewSubPub()
	defer bus.Close(context.Background())

	// Собираем полученные сообщения в срез.
	var got []int

	// Подписываемся на subject "topic".
	sub, err := bus.Subscribe("topic", func(msg interface{}) {
		// приводим msg к int и сохраняем
		got = append(got, msg.(int))
	})
	if err != nil {
		t.Fatalf("Subscribe вернул ошибку: %v", err)
	}
	// Убедимся, что в конце мы отпишемся.
	defer sub.Unsubscribe()

	// Публикуем пять сообщений подряд: 1,2,3,4,5.
	for i := 1; i <= 5; i++ {
		if err := bus.Publish("topic", i); err != nil {
			t.Fatalf("Publish(%d) вернул ошибку: %v", i, err)
		}
	}

	// Дадим немножко времени на доставку.
	time.Sleep(100 * time.Millisecond)

	// Ожидаемый порядок.
	want := []int{1, 2, 3, 4, 5}
	if len(got) != len(want) {
		t.Fatalf("получили %d сообщений, ожидаем %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("сообщение %d: получили %d, но ожидали %d", i, got[i], want[i])
		}
	}
}

// TestSlowSubscriberDoesNotBlockFast проверяет, что медленный подписчик
// не мешает быстрому получать свои сообщения без задержки.
func TestSlowSubscriberDoesNotBlockFast(t *testing.T) {
	bus := NewSubPub()
	defer bus.Close(context.Background())

	// 1) Медленный подписчик: обрабатывает каждое сообщение 200ms.
	_, err := bus.Subscribe("topic", func(msg interface{}) {
		time.Sleep(200 * time.Millisecond)
	})
	if err != nil {
		t.Fatalf("Subscribe (slow) вернул ошибку: %v", err)
	}

	// 2) Быстрый подписчик: сразу увеличивает счётчик.
	var fastCount int
	_, err = bus.Subscribe("topic", func(msg interface{}) {
		fastCount++
	})
	if err != nil {
		t.Fatalf("Subscribe (fast) вернул ошибку: %v", err)
	}

	// Публикуем 5 сообщений подряд.
	for i := 0; i < 5; i++ {
		if err := bus.Publish("topic", i); err != nil {
			t.Fatalf("Publish(%d) вернул ошибку: %v", i, err)
		}
	}

	// Ожидаем, что быстрый подписчик получит все 5 сообщений
	// не дожидаясь медленного. Ждём до 200ms общим таймаутом.
	deadline := time.Now().Add(200 * time.Millisecond)
	for fastCount < 5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if fastCount != 5 {
		t.Errorf("быстрый подписчик получил %d сообщений; ожидалось 5", fastCount)
	}
}

// TestUnsubscribe проверяет, что после вызова Unsubscribe()
// подписчик больше не получает новых сообщений.
func TestUnsubscribe(t *testing.T) {
	bus := NewSubPub()
	defer bus.Close(context.Background())

	// Используем канал с буфером, чтобы колбэк не блокировался.
	ch := make(chan interface{}, 1)
	sub, err := bus.Subscribe("topic", func(msg interface{}) {
		ch <- msg
	})
	if err != nil {
		t.Fatalf("Subscribe вернул ошибку: %v", err)
	}

	// 1) Публикуем «first» и ждём его получения.
	if err := bus.Publish("topic", "first"); err != nil {
		t.Fatalf("Publish(first) вернул ошибку: %v", err)
	}
	select {
	case m := <-ch:
		if m.(string) != "first" {
			t.Errorf("получили %v; ожидали «first»", m)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("не получили первое сообщение")
	}

	// 2) Отписываемся.
	sub.Unsubscribe()

	// 3) Публикуем «second» — он не должен прийти.
	if err := bus.Publish("topic", "second"); err != nil {
		t.Fatalf("Publish(second) вернул ошибку: %v", err)
	}
	select {
	case m := <-ch:
		t.Errorf("получили после Unsubscribe: %v", m)
	case <-time.After(100 * time.Millisecond):
		// правильно, не пришло
	}
}

// TestClose_WaitsForDelivery проверяет, что Close(ctx)
// дожидается доставки уже опубликованных сообщений.
func TestClose_WaitsForDelivery(t *testing.T) {
	bus := NewSubPub()

	// Собираем все числа, которые придут в подписку.
	var got []int
	_, err := bus.Subscribe("topic", func(msg interface{}) {
		got = append(got, msg.(int))
	})
	if err != nil {
		t.Fatalf("Subscribe вернул ошибку: %v", err)
	}

	// Публикуем 3 сообщения.
	for i := 1; i <= 3; i++ {
		if err := bus.Publish("topic", i); err != nil {
			t.Fatalf("Publish(%d) вернул ошибку: %v", i, err)
		}
	}

	// Закрываем шину с таймаутом 1s — ожидаем, что все 3 будут доставлены.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := bus.Close(ctx); err != nil {
		t.Fatalf("Close вернул ошибку: %v", err)
	}

	// Проверяем, что все сообщения попали в got.
	want := []int{1, 2, 3}
	if len(got) != len(want) {
		t.Fatalf("после Close получено %d сообщений; ожидалось %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%d; want %d", i, got[i], want[i])
		}
	}
}

// TestClose_CancelContext проверяет, что если контекст
// был отменён до завершения Close, метод вернёт ошибку
// и завершится быстро.
func TestClose_CancelContext(t *testing.T) {
	bus := NewSubPub()

	// Подписчик висит в длительной обработке.
	_, err := bus.Subscribe("topic", func(msg interface{}) {
		time.Sleep(2 * time.Second)
	})
	if err != nil {
		t.Fatalf("Subscribe вернул ошибку: %v", err)
	}

	// Публикуем, чтобы канал начал обрабатывать.
	if err := bus.Publish("topic", 1); err != nil {
		t.Fatalf("Publish вернул ошибку: %v", err)
	}

	// Создаём контекст и сразу его отменяем.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Вызываем Close и замеряем время.
	start := time.Now()
	err = bus.Close(ctx)
	elapsed := time.Since(start)

	// Ожидаем ошибку по отменённому контексту.
	if err == nil {
		t.Fatal("ожидали ошибку из Close при отменённом контексте; получили nil")
	}
	// И возвращение должно быть быстрым.
	if elapsed > 200*time.Millisecond {
		t.Errorf("Close занял слишком много времени: %v – ожидалось мгновенное завершение", elapsed)
	}
}

// TestGoroutineLeak проверяет, что после подписки, отписки и закрытия
// шины subpub нет утечки горутин.
func TestGoroutineLeak(t *testing.T) {
	// Принудительно запускаем сборщик мусора, чтобы убрать лишние горутины.
	runtime.GC()
	start := runtime.NumGoroutine()

	// Создаём шину и пару подписок.
	bus := NewSubPub()
	sub1, err := bus.Subscribe("leak", func(_ interface{}) {})
	if err != nil {
		t.Fatalf("Subscribe 1 вернул ошибку: %v", err)
	}
	sub2, err := bus.Subscribe("leak", func(_ interface{}) {})
	if err != nil {
		t.Fatalf("Subscribe 2 вернул ошибку: %v", err)
	}

	// Отписываемся сразу.
	sub1.Unsubscribe()
	sub2.Unsubscribe()

	// Закрываем шину с обычным контекстом.
	if err := bus.Close(context.Background()); err != nil {
		t.Fatalf("Close вернул ошибку: %v", err)
	}

	// Дадим горутинам время на выход.
	time.Sleep(50 * time.Millisecond)
	runtime.GC()
	end := runtime.NumGoroutine()

	// Разрешаем небольшое изменение, учитывая служебные горутины тестовой среды.
	if end > start+1 {
		t.Errorf("утечка горутин: было %d, стало %d", start, end)
	}
}
