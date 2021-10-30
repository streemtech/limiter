package main

import (
	"context"
	"fmt"
	"time"

	redis "github.com/go-redis/redis/v8"
	rLimiter "github.com/streemtech/limiter/redis"
)

func main() {

	cli := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Username: "",
		Password: "Password1234",
	})

	fmt.Printf("Hello World\n")

	limiter, _ := rLimiter.NewSlidingWindow(context.Background(), cli, "test:key:", time.Second*3, 5)

	start := time.Now()
	for i := 0; i < 100; i++ {
		<-limiter.Wait()
		fmt.Printf("dome: %d\twaited: %s\n", i, time.Since(start))
		start = time.Now()
		// time.Sleep(time.Millisecond * 100)
	}

}
