package utils

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"time"
)

func Exit() {
	// 短时间连续多次Ctrl+C强制退出机制
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)

	var (
		mu        sync.Mutex
		count     int
		lastPress time.Time
	)

	go func() {
		for range c {
			mu.Lock()

			now := time.Now()
			if now.Sub(lastPress) > 1900*time.Millisecond {
				count = 1
			} else {
				count++
			}
			lastPress = now

			currentCount := count
			mu.Unlock()

			if currentCount >= 2 {
				fmt.Println("Force exiting...")
				fmt.Println("连续2次 Ctrl+C 且每两次之间小于1.9秒将触发强制退出机制")
				time.Sleep(130 * time.Millisecond)
				os.Exit(1)
			}
			//time.Sleep(1 * time.Millisecond)
		}
	}()

}
