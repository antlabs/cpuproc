package main

import (
	"fmt"
	"os"
	"time"

	"github.com/antlabs/cpuproc"
)

func main() {

	p := cpuproc.NewProcess(int32(os.Getpid()))

	go func() {
		for i := 0; i < 8; i++ {
			go func() {
				for {

				}
			}()
		}
	}()
	for {
		fmt.Println(cpuproc.PercentTotal(0))
		fmt.Println(p.CPUPercent())
		time.Sleep(time.Second)
	}
}
