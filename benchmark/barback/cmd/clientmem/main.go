package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"runtime/debug"
	"strconv"
	"syscall"

	whatsmeow "github.com/polymorfa/hypermeow"
	"github.com/polymorfa/hypermeow/store"
	waLog "github.com/polymorfa/hypermeow/util/log"
)

var revision = "working-tree"

type result struct {
	Revision            string `json:"revision"`
	Sessions            int    `json:"sessions"`
	HeapAllocBytes      uint64 `json:"heap_alloc_bytes"`
	HeapInuseBytes      uint64 `json:"heap_inuse_bytes"`
	TotalAllocBytes     uint64 `json:"total_alloc_bytes"`
	HeapAllocPerSession uint64 `json:"heap_alloc_per_session"`
	PeakRSSBytes        int64  `json:"peak_rss_bytes"`
}

func main() {
	sessions, err := strconv.Atoi(env("BENCH_SESSIONS", "2000"))
	if err != nil || sessions < 1 {
		fmt.Fprintln(os.Stderr, "invalid BENCH_SESSIONS")
		os.Exit(2)
	}
	debug.SetGCPercent(-1)
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	device := &store.Device{}
	clients := make([]*whatsmeow.Client, sessions)
	for i := range clients {
		clients[i] = whatsmeow.NewClient(device, waLog.Noop)
	}
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(clients)

	heapAlloc := after.HeapAlloc - before.HeapAlloc
	value := result{
		Revision:            revision,
		Sessions:            sessions,
		HeapAllocBytes:      heapAlloc,
		HeapInuseBytes:      after.HeapInuse - before.HeapInuse,
		TotalAllocBytes:     after.TotalAlloc - before.TotalAlloc,
		HeapAllocPerSession: heapAlloc / uint64(sessions),
		PeakRSSBytes:        peakRSS(),
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		panic(err)
	}
	data = append(data, '\n')
	if err = os.WriteFile(env("RESULT_PATH", "/results/client-memory.json"), data, 0o644); err != nil {
		panic(err)
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func peakRSS() int64 {
	var usage syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) != nil {
		return 0
	}
	return usage.Maxrss * 1024
}
