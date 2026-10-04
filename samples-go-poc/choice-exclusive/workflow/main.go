// The upstream exclusive-choice workflow, adapted to run in an isolate.
package main

import (
	"errors"
	"time"

	"github.com/mfateev/golang-go/sdk-go-poc/workflow"
)

func main() {
	_ = workflow.Complete(nil, placeOrder())
}

func placeOrder() error {
	choice, err := workflow.ExecuteActivity("GetOrder", nil, 10*time.Second)
	if err != nil {
		return err
	}
	var activityName string
	switch string(choice) {
	case "apple":
		activityName = "OrderApple"
	case "banana":
		activityName = "OrderBanana"
	case "cherry":
		activityName = "OrderCherry"
	case "orange":
		activityName = "OrderOrange"
	default:
		return errors.New("unknown order choice: " + string(choice))
	}
	_, err = workflow.ExecuteActivity(activityName, choice, 10*time.Second)
	return err
}
