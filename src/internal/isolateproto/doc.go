// Package isolateproto is the Phase 1, trusted-code reference model for Go
// isolates. It does not modify the Go runtime or isolate package globals.
//
// A Task owns an execution baton. Only one registered task runs at a time.
// Go, Call, Inbox, Yield, Channel operations, and SelectReceive hand the baton
// to the coordinator. FIFO task order and monotonically increasing call IDs
// make those operations reproducible. Task.Now, Task.Sleep, Task.RandUint64,
// and SortedKeys provide narrower deterministic substitutes for clock,
// timer, random, and map iteration behavior.
//
// Native go statements, channel operations, select, sync blocking, ordinary
// or reflected map iteration, reflect.Select, time.Now, time.Sleep, timer
// callbacks, and context deadlines are outside this prototype's contract.
// Package globals and registered adapter closures are shared across instances.
// A task that blocks or loops without reaching a Task operation can prevent
// quiescence. Kill permanently revokes task admission and wakes tasks parked
// in prototype operations. It waits for every registered task to exit, or
// returns KillPendingError if its context expires. A task already executing
// native code can continue until its next Task operation or return. This is
// trusted-code-only machinery and makes no containment claim.
package isolateproto
