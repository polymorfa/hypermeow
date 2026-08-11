// Copyright (c) 2026 Rajeh Taher
//
// Licensed under the MIT License. See LICENSE-MIT for details.

package main

import (
	"os"
	"strconv"
	"strings"
)

type networkStats struct {
	RXBytes   uint64 `json:"rx_bytes"`
	RXPackets uint64 `json:"rx_packets"`
	TXBytes   uint64 `json:"tx_bytes"`
	TXPackets uint64 `json:"tx_packets"`
}

type processIOStats struct {
	ReadChars  uint64 `json:"read_chars"`
	WriteChars uint64 `json:"write_chars"`
	ReadCalls  uint64 `json:"read_calls"`
	WriteCalls uint64 `json:"write_calls"`
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
}

type blockIOStats struct {
	ReadBytes  uint64 `json:"read_bytes"`
	WriteBytes uint64 `json:"write_bytes"`
	ReadOps    uint64 `json:"read_ops"`
	WriteOps   uint64 `json:"write_ops"`
}

type tempIOStats struct {
	PeakBytes  int64 `json:"peak_bytes"`
	PeakFiles  int64 `json:"peak_files"`
	FinalBytes int64 `json:"final_bytes"`
	FinalFiles int64 `json:"final_files"`
}

type resourceStats struct {
	Network networkStats   `json:"network"`
	Process processIOStats `json:"process_io"`
	Block   blockIOStats   `json:"block_io"`
	Temp    tempIOStats    `json:"temporary_files"`
}

func tempDirUsage() (bytes, files int64) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			continue
		}
		info, statErr := entry.Info()
		if statErr == nil {
			bytes += info.Size()
			files++
		}
	}
	return
}

type workloadRuntimeStats struct {
	HeapAllocBytes     uint64  `json:"heap_alloc_bytes"`
	HeapInuseBytes     uint64  `json:"heap_inuse_bytes"`
	TotalAllocBytes    uint64  `json:"total_alloc_bytes"`
	GarbageCollections uint32  `json:"garbage_collections"`
	UserCPUSeconds     float64 `json:"user_cpu_seconds"`
	SysCPUSeconds      float64 `json:"sys_cpu_seconds"`
	PeakRSSBytes       int64   `json:"peak_rss_bytes"`
}

func resourceSnapshot() resourceStats {
	return resourceStats{
		Network: readNetworkStats(),
		Process: readProcessIOStats(),
		Block:   readBlockIOStats(),
	}
}

func resourceDelta(end, start resourceStats) resourceStats {
	return resourceStats{
		Network: networkStats{
			RXBytes:   subtract(end.Network.RXBytes, start.Network.RXBytes),
			RXPackets: subtract(end.Network.RXPackets, start.Network.RXPackets),
			TXBytes:   subtract(end.Network.TXBytes, start.Network.TXBytes),
			TXPackets: subtract(end.Network.TXPackets, start.Network.TXPackets),
		},
		Process: processIOStats{
			ReadChars:  subtract(end.Process.ReadChars, start.Process.ReadChars),
			WriteChars: subtract(end.Process.WriteChars, start.Process.WriteChars),
			ReadCalls:  subtract(end.Process.ReadCalls, start.Process.ReadCalls),
			WriteCalls: subtract(end.Process.WriteCalls, start.Process.WriteCalls),
			ReadBytes:  subtract(end.Process.ReadBytes, start.Process.ReadBytes),
			WriteBytes: subtract(end.Process.WriteBytes, start.Process.WriteBytes),
		},
		Block: blockIOStats{
			ReadBytes:  subtract(end.Block.ReadBytes, start.Block.ReadBytes),
			WriteBytes: subtract(end.Block.WriteBytes, start.Block.WriteBytes),
			ReadOps:    subtract(end.Block.ReadOps, start.Block.ReadOps),
			WriteOps:   subtract(end.Block.WriteOps, start.Block.WriteOps),
		},
	}
}

func runtimeDelta(end, start runtimeStats) workloadRuntimeStats {
	return workloadRuntimeStats{
		HeapAllocBytes:     end.AllocBytes,
		HeapInuseBytes:     end.HeapInuseBytes,
		TotalAllocBytes:    subtract(end.TotalAllocBytes, start.TotalAllocBytes),
		GarbageCollections: uint32(subtract(uint64(end.NumGC), uint64(start.NumGC))),
		UserCPUSeconds:     max(0, end.UserCPUSeconds-start.UserCPUSeconds),
		SysCPUSeconds:      max(0, end.SysCPUSeconds-start.SysCPUSeconds),
		PeakRSSBytes:       end.PeakRSSBytes,
	}
}

func readNetworkStats() (stats networkStats) {
	data, err := os.ReadFile("/proc/self/net/dev")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		name, values, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(name) == "lo" {
			continue
		}
		fields := strings.Fields(values)
		if len(fields) < 10 {
			continue
		}
		stats.RXBytes += parseUint(fields[0])
		stats.RXPackets += parseUint(fields[1])
		stats.TXBytes += parseUint(fields[8])
		stats.TXPackets += parseUint(fields[9])
	}
	return
}

func readProcessIOStats() (stats processIOStats) {
	data, err := os.ReadFile("/proc/self/io")
	if err != nil {
		return
	}
	values := parseKeyValues(string(data))
	stats.ReadChars = values["rchar"]
	stats.WriteChars = values["wchar"]
	stats.ReadCalls = values["syscr"]
	stats.WriteCalls = values["syscw"]
	stats.ReadBytes = values["read_bytes"]
	stats.WriteBytes = values["write_bytes"]
	return
}

func readBlockIOStats() (stats blockIOStats) {
	data, err := os.ReadFile("/sys/fs/cgroup/io.stat")
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		values := parseKeyValues(line)
		stats.ReadBytes += values["rbytes"]
		stats.WriteBytes += values["wbytes"]
		stats.ReadOps += values["rios"]
		stats.WriteOps += values["wios"]
	}
	return
}

func parseKeyValues(value string) map[string]uint64 {
	result := make(map[string]uint64)
	for _, field := range strings.Fields(value) {
		key, raw, ok := strings.Cut(strings.TrimSuffix(field, ":"), "=")
		if !ok {
			continue
		}
		result[key] += parseUint(raw)
	}
	for _, line := range strings.Split(value, "\n") {
		key, raw, ok := strings.Cut(line, ":")
		if ok {
			result[strings.TrimSpace(key)] += parseUint(strings.TrimSpace(raw))
		}
	}
	return result
}

func parseUint(value string) uint64 {
	parsed, _ := strconv.ParseUint(value, 10, 64)
	return parsed
}

func subtract(end, start uint64) uint64 {
	if end < start {
		return 0
	}
	return end - start
}
