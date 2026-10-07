// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolate

import (
	"errors"
	"internal/isolatebridge"
)

// LogOp is the reserved Call operation for printing and standard logging.
const LogOp = isolatebridge.LogOp

// LogRecord contains copied output bytes, formatted inside the instance.
type LogRecord struct{ Source, Message string }

// DecodeLog decodes a host-owned LogOp command. Replies must not expose a sink's
// errors or configuration to workflow code: reply with nil bytes and nil error.
func DecodeLog(payload []byte) (LogRecord, error) {
	if len(payload) == 0 {
		return LogRecord{}, errors.New("isolate: empty logging command")
	}
	sources := [...]string{"fmt", "log", "slog", "builtin"}
	if int(payload[0]) >= len(sources) {
		return LogRecord{}, errors.New("isolate: unknown logging source")
	}
	return LogRecord{Source: sources[payload[0]], Message: string(payload[1:])}, nil
}
