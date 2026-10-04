package main

import (
	"context"
	"isolate"
	"log"
	"os"

	"github.com/mfateev/golang-go/sdk-go-poc/temporalbridge"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func echo(_ context.Context, input []byte) ([]byte, error) { return input, nil }

func main() {
	program, ok := isolate.LookupProgram("temporal-order")
	if !ok {
		log.Fatal("missing temporal-order isolate; build with -isolate-dir=./example/order")
	}
	signalProgram, ok := isolate.LookupProgram("temporal-signal")
	if !ok {
		log.Fatal("missing temporal-signal isolate; build with -isolate-dir=./example/signal")
	}
	address := os.Getenv("TEMPORAL_ADDRESS")
	if address == "" {
		address = "localhost:7233"
	}
	c, err := client.Dial(client.Options{HostPort: address})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	w := worker.New(c, "isolate-poc", worker.Options{})
	temporalbridge.Register(w, "IsolateOrder", program)
	temporalbridge.Register(w, "IsolateSignal", signalProgram)
	w.RegisterActivityWithOptions(echo, activity.RegisterOptions{Name: "echo"})
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatal(err)
	}
}
