package isolateproto

import "encoding/json"

// RegisterJSON adapts a typed entry to the copy-only byte boundary. The
// encoding is an SDK choice; this adapter uses JSON for the prototype.
func RegisterJSON[In, Out any](name string, fn func(*Task, In) (Out, error)) Entry {
	if fn == nil {
		panic("isolateproto: nil typed entry")
	}
	return Register(name, func(task *Task, payload []byte) ([]byte, error) {
		var input In
		if err := json.Unmarshal(payload, &input); err != nil {
			return nil, err
		}
		output, err := fn(task, input)
		if err != nil {
			return nil, err
		}
		return json.Marshal(output)
	})
}

// CallJSON encodes and decodes a typed host operation through Call. Its op
// number is owned by the SDK and must remain stable across builds.
func CallJSON[In, Out any](task *Task, op uint32, input In) (Out, error) {
	var zero Out
	payload, err := json.Marshal(input)
	if err != nil {
		return zero, err
	}
	answer, err := task.Call(op, payload)
	if err != nil {
		return zero, err
	}
	var output Out
	if err := json.Unmarshal(answer, &output); err != nil {
		return zero, err
	}
	return output, nil
}
