//go:build linux

package isolateproto

import (
	"context"
	"os"
	"runtime"
	"runtime/metrics"
	"strconv"
	"strings"
	"testing"
)

// BenchmarkIdleProxy is an opt-in Phase 0 E3 proxy. It measures the process
// increment for 10k prototype instances parked on Inbox. It includes Go
// scheduler goroutines and prototype maps, so it is not a measurement of the
// later owned-span runtime implementation.
func BenchmarkIdleProxy(b *testing.B) {
	benchmarkProxy(b, inboxEntry)
}

// BenchmarkFanoutProxy parks a workflow with two child activity calls and a
// parent channel receive, a more representative suspended continuation.
func BenchmarkFanoutProxy(b *testing.B) {
	benchmarkProxy(b, fanoutEntry)
}

func benchmarkProxy(b *testing.B, entry Entry) {
	const count = 10_000
	for range b.N {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		rssBefore, err := processRSS()
		if err != nil {
			b.Fatal(err)
		}
		instances := make([]*Isolate, 0, count)
		for range count {
			iso, err := New(Config{Entry: entry})
			if err != nil {
				b.Fatal(err)
			}
			state, _, err := iso.Resume(context.Background(), nil)
			if err != nil || state != Quiescent {
				b.Fatalf("state=%v err=%v", state, err)
			}
			instances = append(instances, iso)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		rssAfter, err := processRSS()
		if err != nil {
			b.Fatal(err)
		}
		b.ReportMetric(float64(after.HeapAlloc-before.HeapAlloc)/count, "heap-B/isolate")
		b.ReportMetric(float64(after.StackInuse-before.StackInuse)/count, "stack-B/isolate")
		b.ReportMetric(float64(after.HeapInuse-before.HeapInuse)/count, "inuse-B/isolate")
		b.ReportMetric(float64(rssAfter-rssBefore)/count, "rss-B/isolate")
		b.Logf("10k suspended proxy: HeapAlloc=%d, StackInuse=%d, HeapInuse=%d, RSS=%d, GCPause=%d ns, GCCPUFraction=%g",
			after.HeapAlloc-before.HeapAlloc, after.StackInuse-before.StackInuse,
			after.HeapInuse-before.HeapInuse, rssAfter-rssBefore,
			after.PauseTotalNs-before.PauseTotalNs, after.GCCPUFraction)
		runtime.KeepAlive(instances)
	}
}

func processRSS() (int64, error) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, strconv.ErrSyntax
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * int64(os.Getpagesize()), nil
}

// BenchmarkIdleGC measures complete forced-GC wall time with 10k parked
// one-task instances. Compare it with BenchmarkBaselineGC in a fresh process.
func BenchmarkIdleGC(b *testing.B) {
	instances := make([]*Isolate, 0, 10_000)
	for range 10_000 {
		iso, err := New(Config{Entry: inboxEntry})
		if err != nil {
			b.Fatal(err)
		}
		if state, _, err := iso.Resume(context.Background(), nil); err != nil || state != Quiescent {
			b.Fatalf("state=%v err=%v", state, err)
		}
		instances = append(instances, iso)
	}
	runtime.GC()
	b.ResetTimer()
	for range b.N {
		runtime.GC()
	}
	b.StopTimer()
	runtime.KeepAlive(instances)
	for _, iso := range instances {
		if state, _, err := iso.Resume(context.Background(), []Event{{ID: 0}}); err != nil || state != Completed {
			b.Fatalf("cleanup state=%v err=%v", state, err)
		}
	}
}

func BenchmarkBaselineGC(b *testing.B) {
	runtime.GC()
	b.ResetTimer()
	for range b.N {
		runtime.GC()
	}
}

// BenchmarkIdleGCProfile and BenchmarkBaselineGCProfile expose the GC costs
// hidden by a single wall-time number. Run each in a fresh process with
// -benchtime=20x. They remain Phase 0 proxies for the explicit scheduler.
func BenchmarkIdleGCProfile(b *testing.B)     { benchmarkGCProfile(b, true) }
func BenchmarkBaselineGCProfile(b *testing.B) { benchmarkGCProfile(b, false) }

func benchmarkGCProfile(b *testing.B, idle bool) {
	var instances []*Isolate
	if idle {
		instances = make([]*Isolate, 0, 10_000)
		for range 10_000 {
			iso, err := New(Config{Entry: inboxEntry})
			if err != nil {
				b.Fatal(err)
			}
			if state, _, err := iso.Resume(context.Background(), nil); err != nil || state != Quiescent {
				b.Fatalf("state=%v err=%v", state, err)
			}
			instances = append(instances, iso)
		}
	}
	runtime.GC()
	beforeMetrics := gcProfileMetrics()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	b.ResetTimer()
	for range b.N {
		runtime.GC()
	}
	b.StopTimer()
	runtime.ReadMemStats(&after)
	afterMetrics := gcProfileMetrics()
	cycles := float64(after.NumGC - before.NumGC)
	if cycles == 0 {
		b.Fatal("no GC cycles completed")
	}
	b.ReportMetric(float64(afterMetrics.stackScan), "stack-scan-B")
	b.ReportMetric(float64(afterMetrics.heapScan), "heap-scan-B")
	b.ReportMetric((afterMetrics.markCPU-beforeMetrics.markCPU)*1e3/cycles, "mark-cpu-ms/gc")
	b.ReportMetric((afterMetrics.assistCPU-beforeMetrics.assistCPU)*1e3/cycles, "assist-cpu-ms/gc")
	b.ReportMetric(float64(after.PauseTotalNs-before.PauseTotalNs)/1e6/cycles, "stw-ms/gc")
	b.ReportMetric(float64(after.TotalAlloc-before.TotalAlloc)/cycles, "alloc-B/gc")
	if after.NumGC-before.NumGC <= 256 {
		var maxPause uint64
		for gc := before.NumGC + 1; gc <= after.NumGC; gc++ {
			if pause := after.PauseNs[(gc+255)%256]; pause > maxPause {
				maxPause = pause
			}
		}
		b.ReportMetric(float64(maxPause)/1e6, "max-stw-ms")
	}
	runtime.KeepAlive(instances)
	for _, iso := range instances {
		if state, _, err := iso.Resume(context.Background(), []Event{{ID: 0}}); err != nil || state != Completed {
			b.Fatalf("cleanup state=%v err=%v", state, err)
		}
	}
}

type gcMetrics struct {
	stackScan uint64
	heapScan  uint64
	markCPU   float64
	assistCPU float64
}

func gcProfileMetrics() gcMetrics {
	samples := []metrics.Sample{
		{Name: "/gc/scan/stack:bytes"},
		{Name: "/gc/scan/heap:bytes"},
		{Name: "/cpu/classes/gc/mark/dedicated:cpu-seconds"},
		{Name: "/cpu/classes/gc/mark/assist:cpu-seconds"},
	}
	metrics.Read(samples)
	return gcMetrics{
		stackScan: samples[0].Value.Uint64(),
		heapScan:  samples[1].Value.Uint64(),
		markCPU:   samples[2].Value.Float64(),
		assistCPU: samples[3].Value.Float64(),
	}
}

// BenchmarkIdleAllocationThroughput measures allocation while idle prototype
// goroutines remain roots for concurrent GC. The baseline uses the same
// allocation loop without those roots. The 1 MiB ring forces repeated heap
// allocation and collection rather than compiler-elided scratch storage.
func BenchmarkIdleAllocationThroughput(b *testing.B) {
	benchmarkAllocationThroughput(b, true)
}

func BenchmarkBaselineAllocationThroughput(b *testing.B) {
	benchmarkAllocationThroughput(b, false)
}

func benchmarkAllocationThroughput(b *testing.B, idle bool) {
	var instances []*Isolate
	if idle {
		instances = make([]*Isolate, 0, 10_000)
		for range 10_000 {
			iso, err := New(Config{Entry: inboxEntry})
			if err != nil {
				b.Fatal(err)
			}
			if state, _, err := iso.Resume(context.Background(), nil); err != nil || state != Quiescent {
				b.Fatalf("state=%v err=%v", state, err)
			}
			instances = append(instances, iso)
		}
	}
	const blockSize = 1024
	ring := make([][]byte, 1024)
	runtime.GC()
	before := gcProfileMetrics()
	b.SetBytes(blockSize)
	b.ReportAllocs()
	b.ResetTimer()
	for n := range b.N {
		block := make([]byte, blockSize)
		block[0] = byte(n)
		ring[n&(len(ring)-1)] = block
	}
	b.StopTimer()
	after := gcProfileMetrics()
	megabytes := float64(b.N*blockSize) / (1024 * 1024)
	b.ReportMetric((after.markCPU-before.markCPU)*1e3/megabytes, "mark-cpu-ms/MiB")
	b.ReportMetric((after.assistCPU-before.assistCPU)*1e3/megabytes, "assist-cpu-ms/MiB")
	runtime.KeepAlive(ring)
	runtime.KeepAlive(instances)
	for _, iso := range instances {
		if state, _, err := iso.Resume(context.Background(), []Event{{ID: 0}}); err != nil || state != Completed {
			b.Fatalf("cleanup state=%v err=%v", state, err)
		}
	}
}
