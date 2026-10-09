// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package isolatebridge

// Bound process-owned buffering independently of the private heap limit.
// Full queues and oversized messages are observational losses, never workflow
// inputs. The POC retains at most 4 MiB of payload per boundary.
const (
	writeBufferSize = 64
	maxWriteBytes   = 64 << 10
)

// Message is one copied, process-owned Write. It has no response channel.
type Message struct {
	Op      uint32
	Payload []byte
}

// Write snapshots payload without waiting for receipt or acknowledgment. Its
// nonblocking send has no select poll randomness and cannot influence the
// deterministic workflow select stream. Read-only services may emit messages.
func (b *Boundary) Write(op uint32, payload []byte) {
	b.stopIfRevoked()
	// Keep all allocation and queue bookkeeping process-owned, and finish the
	// bounded operation before revocation may discard this goroutine. No user
	// callback or host code runs within the trusted scope.
	owner := EnterProcess()
	defer LeaveProcess(owner)
	if len(payload) > maxWriteBytes || len(b.writes) == cap(b.writes) {
		b.droppedWrites.Add(1)
		return
	}
	message := &Message{Op: op, Payload: copyBoundaryBytes(payload)}
	select {
	case b.writes <- message:
	default:
		b.droppedWrites.Add(1)
	}
}

// Writes exposes the independent observational stream. Buffered messages
// survive termination because they retain no private pointers. Like Commands,
// this channel is not closed; a host can drain it after Done.
func (b *Boundary) Writes() <-chan *Message { return b.writes }

// DroppedWrites counts full-queue and oversized-message losses for the host.
func (b *Boundary) DroppedWrites() uint64 { return b.droppedWrites.Load() }
