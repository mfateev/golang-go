package main

import (
	"isolate"
	"log"
	"os"

	"github.com/mfateev/golang-go/samples-go-poc/helloworld"
	"github.com/mfateev/golang-go/sdk-go-poc/temporalbridge"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"
)

func main() {
	program, ok := isolate.LookupProgram("helloworld-poc")
	if !ok {
		log.Fatal("missing helloworld-poc; build with -isolate-dir=./helloworld/workflow")
	}
	address := os.Getenv("TEMPORAL_ADDRESS")
	if address == "" {
		address = client.DefaultHostPort
	}
	c, err := client.Dial(client.Options{HostPort: address})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	w := worker.New(c, "hello-world-poc", worker.Options{})
	temporalbridge.Register(w, "HelloWorld", program)
	w.RegisterActivityWithOptions(helloworld.Activity, activity.RegisterOptions{Name: "HelloWorldActivity"})
	if err := w.Run(worker.InterruptCh()); err != nil {
		log.Fatal(err)
	}
}
