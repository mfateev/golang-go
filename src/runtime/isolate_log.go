// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package runtime

import "unsafe"

//go:linkname isolateSetLogTransport
func isolateSetLogTransport(p unsafe.Pointer, write func(byte, []byte)) bool {
	group := (*isolateRevocationGroup)(p)
	if group.live.Load() != 0 || group.writeLog != nil || write == nil {
		return false
	}
	group.writeLog = write
	return true
}

//go:linkname isolateWriteLog
func isolateWriteLog(source byte, message []byte) {
	gp := getg()
	if gp.isolateMetadataDepth != 0 {
		isolateRejectEffect("logging from a metadata service")
	}
	if gp.isolateGroup == nil || gp.isolateGroup.writeLog == nil {
		isolateRejectEffect("logging without a configured host transport")
		return
	}
	isolateCheckHeapAccess(unsafe.Pointer(unsafe.SliceData(message)), uintptr(len(message)), false)
	gp.isolateGroup.writeLog(source, message)
	isolateDiscardIfRevoked()
}

// Compiler-lowered builtins collect one whole print statement without holding
// the process print lock. The complete buffer is allocated under the current instance owner.
func isolatePrintBegin(size int) {
	if !isolateActive() {
		printlock()
		return
	}
	gp := getg()
	if gp.isolateMetadataDepth != 0 || gp.isolateGroup == nil || gp.isolateGroup.writeLog == nil {
		isolateRejectEffect("print without a configured host transport")
		return
	}
	gp.writebuf = make([]byte, 0, size)
	gp.isolatePrinting = true
}

func isolatePrintEnd() {
	gp := getg()
	if !gp.isolatePrinting {
		printunlock()
		return
	}
	message := gp.writebuf
	gp.writebuf = nil
	gp.isolatePrinting = false
	isolateWriteLog(3, message)
}
