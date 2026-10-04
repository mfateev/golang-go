package main

import (
	"time"

	"github.com/mfateev/golang-go/sdk-go-poc/workflow"
)

func main() {
	input, err := workflow.Input()
	if err == nil {
		input, err = workflow.ExecuteActivity("echo", input, time.Minute)
	}
	if err == nil {
		err = workflow.Sleep(time.Second)
	}
	_ = workflow.Complete(input, err)
}
