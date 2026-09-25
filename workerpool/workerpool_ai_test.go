package workerpool

// Супровідні тести до Частини 3, написані ШІ-агентом. Вони перевіряють
// механізм тайм-ауту RunPool окремо від готових тестів репозиторію:
// тип помилки, дедлайн у контексті, звільнення контексту через
// cancel(), відсутність витоку горутин і те, що воркер після
// тайм-ауту одразу бере наступне завдання.

import (
	"context"
	"errors"
	"runtime"
	"testing"
	"time"
)

// collectWithin читає results до закриття каналу. Якщо канал не
// закривається за d, тест провалюється замість того, щоб зависнути.
func collectWithin(t *testing.T, d time.Duration, results <-chan Result) []Result {
	t.Helper()
	var got []Result
	deadline := time.After(d)
	for {
		select {
		case r, ok := <-results:
			if !ok {
				return got
			}
			got = append(got, r)
		case <-deadline:
			t.Fatalf("канал results не закрився за %v; отримано %d результатів", d, len(got))
			return nil
		}
	}
}

// fetchAfter повертає кооперативний Fetch: успіх із size через d або
// ctx.Err(), якщо контекст завершився раніше.
func fetchAfter(d time.Duration, size int) func(ctx context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		select {
		case <-time.After(d):
			return size, nil
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// makeJobs кладе завдання в буферизований канал і закриває його.
func makeJobs(jobs ...Job) <-chan Job {
	ch := make(chan Job, len(jobs))
	for _, j := range jobs {
		ch <- j
	}
	close(ch)
	return ch
}

func TestTimeout_ErrIsDeadlineExceeded(t *testing.T) {
	const timeout = 100 * time.Millisecond
	jobs := makeJobs(Job{ID: "slow", Fetch: fetchAfter(2*time.Second, 1234)})

	start := time.Now()
	got := collectWithin(t, 3*time.Second, RunPool(jobs, 1, timeout))
	elapsed := time.Since(start)

	if len(got) != 1 {
		t.Fatalf("отримано %d результатів, очікувався 1", len(got))
	}
	r := got[0]
	if r.JobID != "slow" {
		t.Errorf("JobID = %q, want %q", r.JobID, "slow")
	}
	if !errors.Is(r.Err, context.DeadlineExceeded) {
		t.Errorf("Err = %v, want context.DeadlineExceeded", r.Err)
	}
	if r.Size != 0 {
		t.Errorf("Size = %d, want 0 при тайм-ауті", r.Size)
	}
	if elapsed < timeout {
		t.Errorf("пул завершився за %v — раніше за тайм-аут %v", elapsed, timeout)
	}
	if elapsed > time.Second {
		t.Errorf("пул тривав %v — мав завершитися близько тайм-ауту %v", elapsed, timeout)
	}
}

func TestTimeout_OnlySlowJobsFail(t *testing.T) {
	const timeout = 100 * time.Millisecond
	jobs := makeJobs(
		Job{ID: "fast-0", Fetch: fetchAfter(5*time.Millisecond, 10)},
		Job{ID: "slow-0", Fetch: fetchAfter(2*time.Second, 20)},
		Job{ID: "fast-1", Fetch: fetchAfter(5*time.Millisecond, 30)},
		Job{ID: "slow-1", Fetch: fetchAfter(2*time.Second, 40)},
		Job{ID: "fast-2", Fetch: fetchAfter(5*time.Millisecond, 50)},
	)

	got := collectWithin(t, 3*time.Second, RunPool(jobs, 3, timeout))

	byID := make(map[string]Result, len(got))
	for _, r := range got {
		if _, dup := byID[r.JobID]; dup {
			t.Errorf("результат для %s отримано двічі", r.JobID)
		}
		byID[r.JobID] = r
	}
	if len(byID) != 5 {
		t.Fatalf("отримано результати для %d завдань, очікувалось 5", len(byID))
	}

	wantSize := map[string]int{"fast-0": 10, "fast-1": 30, "fast-2": 50}
	for id, size := range wantSize {
		r := byID[id]
		if r.Err != nil {
			t.Errorf("%s: неочікувана помилка: %v", id, r.Err)
		}
		if r.Size != size {
			t.Errorf("%s: Size = %d, want %d", id, r.Size, size)
		}
	}
	for _, id := range []string{"slow-0", "slow-1"} {
		if r := byID[id]; !errors.Is(r.Err, context.DeadlineExceeded) {
			t.Errorf("%s: Err = %v, want context.DeadlineExceeded", id, r.Err)
		}
	}
}

// TestTimeout_ContextIsCanceledBeforeNextJob перевіряє дві речі про
// контекст, який воркер передає у Fetch: він має дедлайн у межах
// timeout, і після завершення завдання воркер звільняє його через
// cancel() ще до того, як візьме наступне завдання.
func TestTimeout_ContextIsCanceledBeforeNextJob(t *testing.T) {
	const timeout = 200 * time.Millisecond

	fastCtx := make(chan context.Context, 1)
	slowStarted := make(chan struct{})

	fetchFast := func(ctx context.Context) (int, error) {
		if dl, ok := ctx.Deadline(); !ok {
			t.Error("fast: контекст не має дедлайну")
		} else if left := time.Until(dl); left <= 0 || left > timeout {
			t.Errorf("fast: до дедлайну %v, очікувалось у межах (0, %v]", left, timeout)
		}
		fastCtx <- ctx
		return fetchAfter(5*time.Millisecond, 1)(ctx)
	}
	fetchSlow := func(ctx context.Context) (int, error) {
		close(slowStarted)
		return fetchAfter(2*time.Second, 2)(ctx)
	}

	jobs := makeJobs(Job{ID: "fast", Fetch: fetchFast}, Job{ID: "slow", Fetch: fetchSlow})
	results := RunPool(jobs, 1, timeout)

	var got []Result
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for r := range results {
			got = append(got, r)
		}
	}()

	select {
	case <-slowStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("єдиний воркер не перейшов до другого завдання")
	}

	// Єдиний воркер уже виконує "slow", отже завдання "fast" повністю
	// завершене. Його дедлайн ще не настав, тому стан context.Canceled
	// можливий лише після явного cancel().
	ctx := <-fastCtx
	if err := ctx.Err(); !errors.Is(err, context.Canceled) {
		t.Errorf("fast: ctx.Err() = %v, want context.Canceled — воркер не викликав cancel() після завдання", err)
	}

	select {
	case <-drained:
	case <-time.After(3 * time.Second):
		t.Fatal("канал results не закрився")
	}
	if len(got) != 2 {
		t.Errorf("отримано %d результатів, очікувалось 2", len(got))
	}
}

func TestTimeout_WorkerContinuesAfterTimeout(t *testing.T) {
	const timeout = 100 * time.Millisecond
	jobs := makeJobs(
		Job{ID: "slow", Fetch: fetchAfter(2*time.Second, 1)},
		Job{ID: "fast", Fetch: fetchAfter(5*time.Millisecond, 7)},
	)

	start := time.Now()
	got := collectWithin(t, 3*time.Second, RunPool(jobs, 1, timeout))
	elapsed := time.Since(start)

	if len(got) != 2 {
		t.Fatalf("отримано %d результатів, очікувалось 2", len(got))
	}
	if got[0].JobID != "slow" || !errors.Is(got[0].Err, context.DeadlineExceeded) {
		t.Errorf("перший результат = %+v, очікувався тайм-аут завдання slow", got[0])
	}
	if got[1].JobID != "fast" || got[1].Err != nil || got[1].Size != 7 {
		t.Errorf("другий результат = %+v, очікувався успіх завдання fast із Size 7", got[1])
	}
	if elapsed > time.Second {
		t.Errorf("пул тривав %v — після тайм-ауту воркер мав одразу взяти наступне завдання", elapsed)
	}
}

func TestTimeout_NoGoroutineLeak(t *testing.T) {
	// Примусовий GC заздалегідь запускає фонові горутини збирача, щоб
	// вони не спотворили базову лінію.
	runtime.GC()
	before := runtime.NumGoroutine()

	jobs := makeJobs(
		Job{ID: "slow-0", Fetch: fetchAfter(2*time.Second, 1)},
		Job{ID: "slow-1", Fetch: fetchAfter(2*time.Second, 1)},
		Job{ID: "slow-2", Fetch: fetchAfter(2*time.Second, 1)},
	)
	got := collectWithin(t, 3*time.Second, RunPool(jobs, 3, 50*time.Millisecond))
	if len(got) != 3 {
		t.Fatalf("отримано %d результатів, очікувалось 3", len(got))
	}
	for _, r := range got {
		if !errors.Is(r.Err, context.DeadlineExceeded) {
			t.Errorf("%s: Err = %v, want context.DeadlineExceeded", r.JobID, r.Err)
		}
	}

	// Воркери й горутина, що закриває results, мають завершитися.
	deadline := time.Now().Add(2 * time.Second)
	for {
		after := runtime.NumGoroutine()
		if after <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("витік горутин: до пулу %d, після %d", before, after)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestRunPool_NonPositiveWorkersStillDrainJobs(t *testing.T) {
	jobs := makeJobs(
		Job{ID: "a", Fetch: fetchAfter(time.Millisecond, 1)},
		Job{ID: "b", Fetch: fetchAfter(time.Millisecond, 2)},
	)
	got := collectWithin(t, 3*time.Second, RunPool(jobs, 0, time.Second))
	if len(got) != 2 {
		t.Fatalf("отримано %d результатів, очікувалось 2", len(got))
	}
}

func TestRunPool_NilFetchYieldsError(t *testing.T) {
	jobs := makeJobs(
		Job{ID: "broken"},
		Job{ID: "ok", Fetch: fetchAfter(time.Millisecond, 3)},
	)
	got := collectWithin(t, 3*time.Second, RunPool(jobs, 2, time.Second))
	if len(got) != 2 {
		t.Fatalf("отримано %d результатів, очікувалось 2", len(got))
	}
	byID := make(map[string]Result, len(got))
	for _, r := range got {
		byID[r.JobID] = r
	}
	if r := byID["broken"]; !errors.Is(r.Err, ErrNilFetch) {
		t.Errorf("broken: Err = %v, want ErrNilFetch", r.Err)
	}
	if r := byID["ok"]; r.Err != nil || r.Size != 3 {
		t.Errorf("ok: %+v, очікувався Size 3 без помилки", r)
	}
}
