package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	redis "github.com/redis/go-redis/v9"
	rLimiter "github.com/streemtech/limiter/redis"
)

func main() {

	cli := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Username: "",
		Password: "Password1234",
	})

	fmt.Printf("Hello World\n")

	limiter, _ := rLimiter.NewSlidingWindow(context.Background(), cli, "test:key:", time.Millisecond*1000, 5)

	wg := &sync.WaitGroup{}
	wg.Add(3)
	// time.Sleep(time.Millisecond * 100)
	go newFunction(limiter, "0", wg)
	go newFunction(limiter, "1", wg)
	go newFunction(limiter, "2", wg)
	wg.Wait()

}

func newFunction(limiter *rLimiter.SlidingWindowLimiter, id string, wg *sync.WaitGroup) {
	start := time.Now()
	defer wg.Done()

	for i := 0; i < 25; i++ {
		<-limiter.Wait()
		fmt.Printf("ID %s done: %03d\twaited: %s\n", id, i, time.Since(start))
		start = time.Now()
	}
	fmt.Printf("%s done\n", id)
}
