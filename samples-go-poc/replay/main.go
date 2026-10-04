// Replay a completed sample from a CLI-exported Temporal history.
package main

import (
	"fmt"
	"isolate"
	"os"

	"github.com/mfateev/golang-go/sdk-go-poc/temporalbridge"
	"go.temporal.io/sdk/worker"
	goWorkflow "go.temporal.io/sdk/workflow"
)

func main() {
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: replay <program-name> <workflow-type> <history.json>")
		os.Exit(2)
	}
	program, ok := isolate.LookupProgram(os.Args[1])
	if !ok {
		fmt.Fprintln(os.Stderr, "unknown isolate program:", os.Args[1])
		os.Exit(2)
	}
	replayer := worker.NewWorkflowReplayer()
	replayer.RegisterWorkflowWithOptions(temporalbridge.Factory{Program: program}, goWorkflow.RegisterOptions{Name: os.Args[2]})
	if err := replayer.ReplayWorkflowHistoryFromJSONFile(nil, os.Args[3]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("replay passed:", os.Args[2])
}
