// The upstream helloworld workflow, adapted to run as an ordinary isolate main.
package main

import (
	"time"

	"github.com/mfateev/golang-go/sdk-go-poc/workflow"
)

func main() {
	name, err := workflow.Input()
	var greeting []byte
	if err == nil {
		greeting, err = workflow.ExecuteActivity("HelloWorldActivity", name, 10*time.Second)
	}
	_ = workflow.Complete(greeting, err)
}
