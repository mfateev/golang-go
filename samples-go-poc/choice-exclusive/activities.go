package choiceexclusive

import (
	"context"
	"errors"
	"math/rand"

	"go.temporal.io/sdk/activity"
)

var orderChoices = [...]string{"apple", "banana", "cherry", "orange"}

// GetOrder chooses an order on the host side, as in the upstream sample.
func GetOrder(_ context.Context, _ []byte) ([]byte, error) {
	return []byte(orderChoices[rand.Intn(len(orderChoices))]), nil
}

func order(ctx context.Context, choice []byte, expected string) ([]byte, error) {
	if string(choice) != expected {
		return nil, errors.New("unexpected order choice: " + string(choice))
	}
	activity.GetLogger(ctx).Info("Order choice", "choice", expected)
	return nil, nil
}

func OrderApple(ctx context.Context, choice []byte) ([]byte, error) {
	return order(ctx, choice, "apple")
}

func OrderBanana(ctx context.Context, choice []byte) ([]byte, error) {
	return order(ctx, choice, "banana")
}

func OrderCherry(ctx context.Context, choice []byte) ([]byte, error) {
	return order(ctx, choice, "cherry")
}

func OrderOrange(ctx context.Context, choice []byte) ([]byte, error) {
	return order(ctx, choice, "orange")
}
