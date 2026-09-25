// Package workerpool реалізує Частину 3 домашньої роботи: пул
// воркерів з обмеженим часом на кожне завдання, що симулює
// конкурентне отримання розміру URL.
package workerpool

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrNilFetch потрапляє в Result.Err для Job без функції Fetch:
// таке завдання не виконується, але пул не панікує і повертає
// результат для нього, як і для решти.
var ErrNilFetch = errors.New("workerpool: job has nil Fetch")

// Job — одна одиниця роботи для пулу. Fetch отримує контекст із
// дедлайном і має сам його поважати (кооперативне скасування) —
// саме так уникають витоку горутин при тайм-ауті.
type Job struct {
	ID    string
	Fetch func(ctx context.Context) (int, error)
}

// Result — результат виконання одного Job.
type Result struct {
	JobID string
	Size  int
	Err   error
}

// RunPool запускає рівно numWorkers горутин-воркерів (значення менше
// 1 трактується як 1), які беруть завдання з jobs і надсилають по
// одному Result на кожен Job у повернутий канал. Порядок результатів
// не гарантується.
//
// Кожному Job надається не більше timeout часу: воркер створює
// контекст через context.WithTimeout, передає його в job.Fetch і
// звільняє через cancel() одразу після повернення. Скасування
// кооперативне — Fetch має сам стежити за ctx.Done() і повертати
// ctx.Err(), тоді Result.Err міститиме context.DeadlineExceeded.
// Fetch, що ігнорує контекст, блокує свого воркера до власного
// завершення, але витоку горутин не спричиняє.
//
// Канал результатів закривається, коли jobs закрито й усі воркери
// завершили роботу. Викликач має вичитати результати до кінця,
// інакше воркери заблокуються на надсиланні.
func RunPool(jobs <-chan Job, numWorkers int, timeout time.Duration) <-chan Result {
	if numWorkers < 1 {
		numWorkers = 1
	}
	results := make(chan Result)

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			for job := range jobs {
				results <- runJob(job, timeout)
			}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	return results
}

// runJob виконує одне завдання з обмеженням timeout і звільняє
// контекст одразу після повернення Fetch, щоб не тримати таймер.
func runJob(job Job, timeout time.Duration) Result {
	if job.Fetch == nil {
		return Result{JobID: job.ID, Err: ErrNilFetch}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	size, err := job.Fetch(ctx)
	return Result{JobID: job.ID, Size: size, Err: err}
}
