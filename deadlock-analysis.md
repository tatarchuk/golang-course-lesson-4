# Аналіз дедлоку (Частина 2)

## Код, що аналізується

```go
package main

import (
	"fmt"
	"sync"
)

func main() {
	var wg sync.WaitGroup
	ch := make(chan int) // небуферизований канал

	wg.Add(1)
	go func() {
		// симуляція важкої роботи
		ch <- 42
		wg.Done()
	}()

	wg.Wait()
	fmt.Println(<-ch)
}
```

## Мій промпт до ШІ

Вставте нижче точний текст промпту, який ви розробили. Промпт має
явно вимагати від ШІ:

- **не просто переписати код**, а пояснити точну послідовність
  переплетення (interleaving) горутин, що веде до зависання;
- проаналізувати, чому виклик `wg.Wait()` перед читанням з
  небуферизованого каналу зупиняє виконання.

### Мій промпт

read the code sample with deadlock. explain to me why Goroutins are waiting for each other 
and wg.Wait() stops before the reading from non-bufferred channel.

## Пояснення від ШІ

Вставте нижче відповідь ШІ-асистента на ваш промпт — покрокове
пояснення переплетення горутин.

### Відповідь ШІ

The two rules that matter. A send on an unbuffered channel does not store the value anywhere. It blocks the sending goroutine until some other goroutine executes a receive on the same channel, and only then does the value
pass from one to the other. wg.Wait() blocks the calling goroutine until the WaitGroup counter reaches zero, and the counter reaches zero only when wg.Done() runs. Both operations block without any timeout.

Step by step.

2. Main starts the second goroutine with go. Starting it does not run it to completion. Main continues immediately, and the new goroutine is merely runnable.
3. Main reaches wg.Wait(). The counter is 1, so main parks. It is asleep on the WaitGroup, and it has not executed <-ch, because that line comes only after Wait returns.
4. The second goroutine runs and reaches ch <- 42. The channel has no buffer, and no goroutine is receiving from it right now. Main is asleep in Wait, not in a receive. So the send blocks, and this goroutine parks on the
   channel.
5. The second goroutine is stuck on the line before wg.Done(), so Done never runs and the counter stays at 1.
6. Because the counter stays at 1, main never returns from Wait, so it never reaches <-ch.

Why they wait for each other. Main waits for the counter to drop, which requires the second goroutine to get past its send. The second goroutine waits for a receiver, and the only receive in the program sits in main, after
Wait. Each goroutine can be unblocked only by an action the other one is prevented from taking. That is a cycle of dependencies, which is the definition of a deadlock. In the standalone program every goroutine is asleep, so
the runtime aborts with the message "fatal error: all goroutines are asleep - deadlock!". Its trace shows main inside sync.(*WaitGroup).Wait and the other goroutine inside chan send, which are exactly the two parked states
above. Inside the test the runtime detector stays silent, because the test goroutine has a pending timer, so the 2-second timeout is what reports the hang.

Why Wait before the receive is the cause. The program sequences its operations so that the receive happens after Wait returns, Wait returns only after Done, Done runs only after the send completes, and the send completes only
when the receive happens. As a chain: receive needs Wait, Wait needs Done, Done needs send, send needs receive. The chain closes on itself, and placing wg.Wait() ahead of <-ch is the link that closes it.

Why timing does not save it. Scheduling order between the two goroutines does not matter. If the second goroutine runs first, it blocks on the send before main has reached the receive, and then main blocks in Wait. If main
runs first, it blocks in Wait, and then the goroutine blocks on the send. Every interleaving ends in the same two parked states, which is why the assignment says the code is guaranteed to hang, not merely likely to.

What the cycle depends on. Two facts together produce the hang: the channel has no buffer, so the send cannot complete without a concurrent receiver, and the receive is sequenced after Wait. Remove either fact and the chain
no longer closes. With a buffer, the send completes on its own, Done runs, Wait returns, and main reads the stored value. With the receive placed ahead of Wait, main meets the sender at the channel, the value passes, the
goroutine reaches Done, and Wait returns at once. For a single value the WaitGroup adds nothing, because a completed receive already proves the goroutine finished its send.

## Мінімальне виправлення

Робочу, тестовану версію виправлення реалізовано в
[`deadlock/fixed.go`](./deadlock/fixed.go) (функція `Run`), а
автоматична перевірка — у `deadlock/fixed_test.go`. Коротко опишіть
своїми словами, яку зміну порядку операцій (чи структури каналу) ви
застосували і чому саме вона усуває циклічне очікування.

I've introduced 2 Goroutins: first for writing into the channel, second one for the reading from the channel.
wg.Wait() should come with defer not to cause a deadlock.
The sequence is: writing to the channel started -> waiting -> reading -> writing finished -> waiting released -> channel closed
Deadlock is gone due to 'handshake' established between two Goroutines
