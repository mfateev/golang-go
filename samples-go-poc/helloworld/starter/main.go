package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go.temporal.io/sdk/client"
)

func main() {
	address := os.Getenv("TEMPORAL_ADDRESS")
	if address == "" {
		address = client.DefaultHostPort
	}
	c, err := client.Dial(client.Options{HostPort: address})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	name := "Temporal"
	if len(os.Args) > 1 {
		name = os.Args[1]
	}
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        fmt.Sprintf("hello-world-poc-%d", time.Now().UnixNano()),
		TaskQueue: "hello-world-poc",
	}, "HelloWorld", []byte(name))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("workflow ID:", run.GetID())
	fmt.Println("run ID:", run.GetRunID())
	var result []byte
	if err := run.Get(ctx, &result); err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(result))
}
