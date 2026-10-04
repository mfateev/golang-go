package helloworld

import "context"

// Activity returns the same greeting as the upstream helloworld sample.
func Activity(_ context.Context, name []byte) ([]byte, error) {
	return []byte("Hello " + string(name) + "!"), nil
}
