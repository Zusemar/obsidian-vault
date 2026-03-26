_Стоит сказать что этот примитив не встретится в большинстве языков, он появился в го "derived from Hoare’s CSP." CSP - Communicating Sequential Processes - это модель конкурентного программирования, в которой взаимодействие между независимыми процессами осуществляется через передачу сообщений, а не через общую память._


Канал — это объект связи, с помощью которого горутины обмениваются данными. Технически это конвейер (или труба), откуда можно считывать или помещать данные. То есть одна горутина может отправить данные в канал, а другая — считать помещенные в этот канал данные.

```go
package main

import "fmt"

func main() {    
	c := make(chan int)    
	
	fmt.Printf("type of `c` is %T\n", c)    
	fmt.Printf("value of `c` is %v\n", c)}
```

Запись чтение из канала 
```go
data := <-channel // считываем в переменную из канала
channel <- data // записываем дату в канал
```

Запись и чтение из канала являются блокируемыми. Когда вы помещаете данные в канал, горутина блокируется до тех пор, пока данные не будут считаны другой горутиной из этого канала. В то же время операции канала говорят планировщику о планировании другой горутины, поэтому программа не будет заблокирована полностью. Эти функции весьма полезны, так как отпадает необходимость писать блокировки для взаимодействия горутин.

Закрытие канала
```go
package main

import "fmt"

func greet(c chan string) {
	<-c // for John
	<-c // for Mike
}

func main() {
	fmt.Println("main() started")

	c := make(chan string, 1)

	go greet(c)
	c <- "John"

	close(c) // closing channel

	c <- "Mike"
	fmt.Println("main() stopped")
}
```
output
```
main() started
panic: send on closed channel

goroutine 1 [running]:
main.main()
	/tmp/sandbox3574558810/prog.go:20 +0xc8
Program exited.
```
Как понятно их примера записывать в закрытый канал нельзя

Как и со всеми примитивами конкурентности, с каналами могут возникать проблемы. Одна из них - deadlock
##### Deadlock (Взаимная блокировка)



###### Однонаправленные каналы
Канал который сможет только считывать данные или только записывать их.

Однонаправленный канал также создается с использованием `make`, но с дополнительным стрелочным синтаксисом.
```go
roc := make(<-chan int)
soc := make(chan<- int)

fmt.Printf("Data type of roc is `%T`\n", roc)
fmt.Printf("Data type of soc is `%T\n", soc)
```
вывод
```
Data type of roc is `<-chan int`
Data type of soc is `chan<- int
```
Получается что такие каналы имеют разные типы

Но в чем смысл использования однонаправленного канала? Использование однонаправленного канала улучшает безопасность типов в программe, что, как следствие, порождает меньше ошибок.
А в нужный момент мы можем поменять тип канала


## Select

похож на `switch` без аргументов, но он может использоваться только для операций с каналами. Оператор `select` используется для выполнения операции только с одним из множества каналов, условно выбранного блоком case.

```go
package main

import (
	"fmt"
	"time"
)

var start time.Time
func init() {
	start = time.Now()
}

func service1(c chan string) {
	time.Sleep(3 * time.Second)
	c <- "Hello from service 1"
}

func service2(c chan string) {
	time.Sleep(5 * time.Second)
	c <- "Hello from service 2"
}

func main() {
	fmt.Println("main() started", time.Since(start))

	chan1 := make(chan string)
	chan2 := make(chan string)

	go service1(chan1)
	go service2(chan2)

	select {
	case res := <-chan1:
		fmt.Println("Response from service 1", res, time.Since(start))
	case res := <-chan2:
		fmt.Println("Response from service 2", res, time.Since(start))
	}
	
	fmt.Println("main() stopped", time.Since(start))
}
```

Вышеприведенная программа имитирует реальный веб-сервис, в котором балансировщик нагрузки получает миллионы запросов и должен возвращать ответ от одной из доступных служб. Используя стандартные горутины, каналы и select, мы можем запросить ответ у нескольких сервисов, и тот, который ответит раньше всех, может быть использован.

##### default case
Так же как и `switch`, оператор `select` поддерживает оператор `default`. Оператор `default` является неблокируемым, но это еще не все, оператор `default` делает блок `select` всегда неблокируемым. Это означает, что операции отправки и чтение на любом канале (не имеет значения будет ли канал с буфером или без) всегда будут неблокируемыми.

```go
package main

import (
	"fmt"
	"time"
)

var start time.Time

func init() {
	start = time.Now()
}

func service1(c chan string) {
	fmt.Println("service1() started", time.Since(start))
	c <- "Hello from service 1"
}

func service2(c chan string) {
	fmt.Println("service2() started", time.Since(start))
	c <- "Hello from service 2"
}

func main() {
	fmt.Println("main() started", time.Since(start))
	
	chan1 := make(chan string)
	chan2 := make(chan string)

	go service1(chan1)
	go service2(chan2)

	select {
	case res := <-chan1:
		fmt.Println("Response from service 1", res, time.Since(start))
	case res := <-chan2:
		fmt.Println("Response from service 2", res, time.Since(start))
	default:
		fmt.Println("No response received", time.Since(start))
	}

	fmt.Println("main() stopped", time.Since(start))
}

```
вывод
```
main() started 0s
No response received 0s
main() stopped 0s
```
Так как в приведенной программе каналы используются без буфера, и значение еще отсутствует, в обоих каналах будет исполнен `default`. Если бы в блоке `select` отсутствовал `default`, то произошла бы блокировка и результат был бы другим.

---

#golang #concurrency #channel

## Связанные темы

- [[go goroutine]] — горутины обмениваются данными через каналы; `sudog` в очередях ожидания
- [[go scheduler]] — блокировка на канале как сигнал планировщику переключиться
- [[go Interfaces]] — `io.Reader` / `io.Writer` используют похожие паттерны потоков данных
- [[Go — sync atomic (атомики)]] — альтернатива каналам для простых флагов состояния
- [[sync.Mutex]] — альтернативный примитив синхронизации
