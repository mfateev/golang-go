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
	run, err := c.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        fmt.Sprintf("choice-exclusive-poc-%d", time.Now().UnixNano()),
		TaskQueue: "choice-exclusive-poc",
	}, "ExclusiveChoice")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("workflow ID:", run.GetID())
	fmt.Println("run ID:", run.GetRunID())
	if err := run.Get(ctx, nil); err != nil {
		log.Fatal(err)
	}
	fmt.Println("order completed")
}
