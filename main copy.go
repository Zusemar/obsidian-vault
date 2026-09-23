package main

import (
	"fmt"
	"runtime"
	"time"
)

func main() {
	runtime.GOMAXPROCS(1)
	go func() { fmt.Println(1) }()
	go func() { fmt.Println(2) }()
	go func() { fmt.Println(3) }()
	go func() { fmt.Println(4) }()
	<-time.After(10 * time.Millisecond)
}
