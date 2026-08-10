package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

var revision = "working-tree"

type config struct {
	DatabaseURL    string
	BarbackURL     string
	BarbackWS      string
	TLSCAPath      string
	TLSServerName  string
	OutputPath     string
	MemProfilePath string
	Variant        string
	BusinessSmoke  bool
	Total          int64
	Timeout        time.Duration
	Workload       workloadConfig
}

type workloadConfig struct {
	Scenario             string `json:"scenario"`
	Mode                 string `json:"mode"`
	Rate                 int64  `json:"rate"`
	Senders              int64  `json:"senders"`
	Workers              int64  `json:"workers"`
	WarmupMS             int64  `json:"warmup_ms"`
	GroupSize            int64  `json:"group_size,omitempty"`
	HistoryConversations int64  `json:"history_conversations"`
	HistoryMessages      int64  `json:"history_messages_per_conversation"`
	MessageProfile       string `json:"message_profile"`
}

type queryStat struct {
	Query       string  `json:"query"`
	Calls       int64   `json:"calls"`
	TotalExecMS float64 `json:"total_exec_ms"`
	Rows        int64   `json:"rows"`
}

type databaseStats struct {
	Calls       int64       `json:"calls"`
	TotalExecMS float64     `json:"total_exec_ms"`
	Rows        int64       `json:"rows"`
	SizeBytes   int64       `json:"size_bytes"`
	Queries     []queryStat `json:"queries"`
}

type latencyStats struct {
	MinMS float64 `json:"min_ms"`
	P50MS float64 `json:"p50_ms"`
	P95MS float64 `json:"p95_ms"`
	P99MS float64 `json:"p99_ms"`
	MaxMS float64 `json:"max_ms"`
}

type runtimeStats struct {
	AllocBytes      uint64  `json:"alloc_bytes"`
	HeapInuseBytes  uint64  `json:"heap_inuse_bytes"`
	SysBytes        uint64  `json:"sys_bytes"`
	TotalAllocBytes uint64  `json:"total_alloc_bytes"`
	NumGC           uint32  `json:"num_gc"`
	Goroutines      int     `json:"goroutines"`
	PeakRSSBytes    int64   `json:"peak_rss_bytes"`
	UserCPUSeconds  float64 `json:"user_cpu_seconds"`
	SysCPUSeconds   float64 `json:"sys_cpu_seconds"`
}

type result struct {
	Variant              string               `json:"variant"`
	Revision             string               `json:"revision"`
	StartedAt            time.Time            `json:"started_at"`
	Completed            bool                 `json:"completed"`
	Error                string               `json:"error,omitempty"`
	TargetMessages       int64                `json:"target_messages"`
	Workload             workloadConfig       `json:"workload"`
	MessagesReceived     int64                `json:"messages_received"`
	MessagesSent         int64                `json:"messages_sent"`
	SendFailures         int64                `json:"send_failures"`
	FailureReasons       map[string]int64     `json:"failure_reasons,omitempty"`
	QueueOverflows       int64                `json:"queue_overflows"`
	HistorySyncs         int64                `json:"history_syncs"`
	HistoryConversations int64                `json:"history_conversations"`
	HistoryMessages      int64                `json:"history_messages"`
	DurationMS           float64              `json:"duration_ms"`
	ThroughputPerSec     float64              `json:"throughput_per_sec"`
	SendLatency          latencyStats         `json:"send_latency"`
	Database             databaseStats        `json:"database"`
	Runtime              runtimeStats         `json:"runtime"`
	WorkloadRuntime      workloadRuntimeStats `json:"workload_runtime"`
	SessionRuntime       workloadRuntimeStats `json:"session_runtime"`
	Resources            resourceStats        `json:"resources"`
	MessageTypes         map[string]int64     `json:"message_types"`
	MediaUploads         int64                `json:"media_uploads"`
	MediaUploadBytes     int64                `json:"media_upload_bytes"`
	MediaUploadLatency   latencyStats         `json:"media_upload_latency"`
	BusinessAppValidated bool                 `json:"business_app_validated"`
}

type runner struct {
	cfg        config
	db         *sql.DB
	httpClient *http.Client

	received        atomic.Int64
	sent            atomic.Int64
	failed          atomic.Int64
	overflows       atomic.Int64
	historySyncs    atomic.Int64
	historyConvs    atomic.Int64
	historyMessages atomic.Int64
	messageSequence atomic.Int64
	mediaUploads    atomic.Int64
	mediaBytes      atomic.Int64
	businessValid   atomic.Bool

	startOnce  sync.Once
	doneOnce   sync.Once
	startedAt  atomic.Int64
	finishedAt atomic.Int64
	done       chan struct{}
	jobs       []chan *events.Message

	latencyMu       sync.Mutex
	latencies       []float64
	uploadLatencies []float64
	messageTypes    map[string]int64
	failureReasons  map[string]int64

	metricsMu      sync.Mutex
	runtimeStart   runtimeStats
	sessionStart   runtimeStats
	resourceStart  resourceStats
	metricsStarted bool
	sessionStarted bool
	tempStop       chan struct{}
	tempPeakBytes  atomic.Int64
	tempPeakFiles  atomic.Int64
	connected      chan struct{}
	connectedOnce  sync.Once
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if cfg.MemProfilePath != "" {
		runtime.MemProfileRate = 1
	}
	workerCount := int(cfg.Workload.Workers)
	queueCapacity := (4096 + workerCount - 1) / workerCount
	if queueCapacity < 512 {
		queueCapacity = 512
	}
	r := &runner{
		cfg:            cfg,
		done:           make(chan struct{}),
		jobs:           make([]chan *events.Message, workerCount),
		latencies:      make([]float64, 0, cfg.Total),
		messageTypes:   make(map[string]int64),
		failureReasons: make(map[string]int64),
		connected:      make(chan struct{}),
	}
	for i := range r.jobs {
		r.jobs[i] = make(chan *events.Message, queueCapacity)
	}
	res, runErr := r.run()
	if cfg.MemProfilePath != "" {
		if err = writeMemoryProfile(cfg.MemProfilePath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}
	if runErr != nil {
		res.Error = runErr.Error()
	}
	if err = writeResult(cfg.OutputPath, res); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	total, err := strconv.ParseInt(env("BENCH_TOTAL", "200"), 10, 64)
	if err != nil || total < 1 {
		return config{}, fmt.Errorf("invalid BENCH_TOTAL")
	}
	timeout, err := time.ParseDuration(env("BENCH_TIMEOUT", "5m"))
	if err != nil {
		return config{}, fmt.Errorf("invalid BENCH_TIMEOUT: %w", err)
	}
	mode := env("BENCH_MODE", "group")
	if mode != "dm" && mode != "group" {
		return config{}, fmt.Errorf("invalid BENCH_MODE: must be dm or group")
	}
	messageProfile := env("BENCH_MESSAGE_PROFILE", "text")
	if messageProfile != "text" && messageProfile != "mixed" {
		return config{}, fmt.Errorf("invalid BENCH_MESSAGE_PROFILE: must be text or mixed")
	}
	rate, err := positiveIntEnv("BENCH_RATE", "50")
	if err != nil {
		return config{}, err
	}
	senders, err := positiveIntEnv("BENCH_SENDERS", "1")
	if err != nil {
		return config{}, err
	}
	workers, err := positiveIntEnv("BENCH_WORKERS", "1")
	if err != nil {
		return config{}, err
	}
	warmupMS, err := nonNegativeIntEnv("BENCH_WARMUP_MS", "3000")
	if err != nil {
		return config{}, err
	}
	groupSize, err := nonNegativeIntEnv("BENCH_GROUP_SIZE", "128")
	if err != nil {
		return config{}, err
	}
	historyConversations, err := nonNegativeIntEnv("HISTORY_CONVERSATIONS", "100")
	if err != nil {
		return config{}, err
	}
	historyMessages, err := nonNegativeIntEnv("HISTORY_MESSAGES", "20")
	if err != nil {
		return config{}, err
	}
	businessSmoke, err := strconv.ParseBool(env("BENCH_BUSINESS_SMOKE", "false"))
	if err != nil {
		return config{}, fmt.Errorf("invalid BENCH_BUSINESS_SMOKE")
	}
	return config{
		DatabaseURL:    env("DATABASE_URL", "postgres://postgres:postgres@postgres:5432/hypermeow?sslmode=disable"),
		BarbackURL:     env("BARBACK_URL", "http://barback:8080"),
		BarbackWS:      env("BARBACK_WS", "ws://barback:8080/ws/chat"),
		TLSCAPath:      os.Getenv("BARBACK_TLS_CA"),
		TLSServerName:  env("BARBACK_TLS_SERVER_NAME", "0.0.0.0"),
		OutputPath:     env("RESULT_PATH", "/results/result.json"),
		MemProfilePath: os.Getenv("MEM_PROFILE_PATH"),
		Variant:        env("BENCH_VARIANT", "candidate"),
		BusinessSmoke:  businessSmoke,
		Total:          total,
		Timeout:        timeout,
		Workload: workloadConfig{
			Scenario:             env("BENCH_SCENARIO", "custom"),
			Mode:                 mode,
			Rate:                 rate,
			Senders:              senders,
			Workers:              workers,
			WarmupMS:             warmupMS,
			GroupSize:            groupSize,
			HistoryConversations: historyConversations,
			HistoryMessages:      historyMessages,
			MessageProfile:       messageProfile,
		},
	}, nil
}

func positiveIntEnv(name, fallback string) (int64, error) {
	value, err := strconv.ParseInt(env(name, fallback), 10, 64)
	if err != nil || value < 1 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func nonNegativeIntEnv(name, fallback string) (int64, error) {
	value, err := strconv.ParseInt(env(name, fallback), 10, 64)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid %s", name)
	}
	return value, nil
}

func writeMemoryProfile(path string) error {
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create memory profile: %w", err)
	}
	runtime.GC()
	if err = pprof.WriteHeapProfile(file); err != nil {
		_ = file.Close()
		return fmt.Errorf("write memory profile: %w", err)
	}
	if err = file.Close(); err != nil {
		return fmt.Errorf("close memory profile: %w", err)
	}
	return nil
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func (r *runner) run() (result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), r.cfg.Timeout)
	defer cancel()
	defer r.stopMetricsSampler()

	db, err := sql.Open("pgx", r.cfg.DatabaseURL)
	if err != nil {
		return r.snapshot(false), err
	}
	r.db = db
	defer db.Close()
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(time.Minute)

	container := sqlstore.NewWithDB(db, "postgres", waLog.Noop)
	if err = container.Upgrade(ctx); err != nil {
		return r.snapshot(false), fmt.Errorf("upgrade store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		return r.snapshot(false), fmt.Errorf("load device: %w", err)
	}

	client := whatsmeow.NewClient(device, waLog.Stdout("HyperMeow", "INFO", true))
	if r.cfg.TLSCAPath != "" {
		r.httpClient, err = trustedHTTPClient(r.cfg.TLSCAPath, r.cfg.TLSServerName)
		if err != nil {
			return r.snapshot(false), err
		}
		client.SetMediaHTTPClient(r.httpClient)
		client.SetWebsocketHTTPClient(r.httpClient)
		client.SetPreLoginHTTPClient(r.httpClient)
	} else {
		r.httpClient = http.DefaultClient
	}
	root := barbackRootKey()
	client.SocketConfig = &whatsmeow.SocketConfig{
		URL:                       r.cfg.BarbackWS,
		Origin:                    "https://web.whatsapp.com",
		NoiseCertificateAuthority: &root,
	}
	client.EnableAutoReconnect = true
	client.AddEventHandler(r.handler(client))

	workerCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	for _, jobs := range r.jobs {
		go r.sendWorker(workerCtx, client, jobs)
	}

	if err = client.ConnectContext(ctx); err != nil {
		return r.snapshot(false), fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()
	if r.cfg.BusinessSmoke {
		connectionTimer := time.NewTimer(30 * time.Second)
		defer connectionTimer.Stop()
		select {
		case <-r.connected:
		case <-connectionTimer.C:
			return r.snapshot(false), fmt.Errorf("business app validation: connection did not become ready")
		case <-ctx.Done():
			return r.snapshot(false), fmt.Errorf("business app validation: connection did not become ready: %w", ctx.Err())
		}
		if err = validateBusinessApp(ctx, client); err != nil {
			return r.snapshot(false), err
		}
		r.businessValid.Store(true)
	}

	select {
	case <-r.done:
		time.Sleep(2 * time.Second)
		res := r.snapshot(true)
		return res, nil
	case <-ctx.Done():
		return r.snapshot(false), fmt.Errorf("benchmark incomplete: %w", ctx.Err())
	}
}

func (r *runner) stopMetricsSampler() {
	r.metricsMu.Lock()
	if r.tempStop != nil {
		close(r.tempStop)
		r.tempStop = nil
	}
	r.metricsMu.Unlock()
}

func (r *runner) handler(client *whatsmeow.Client) whatsmeow.EventHandler {
	var scanOnce sync.Once
	return func(evt any) {
		switch event := evt.(type) {
		case *events.QR:
			if len(event.Codes) > 0 {
				scanOnce.Do(func() { go r.scanQR(event.Codes[0]) })
			}
		case *events.Connected:
			r.connectedOnce.Do(func() {
				if r.connected != nil {
					close(r.connected)
				}
			})
			if _, err := r.db.ExecContext(context.Background(), "SELECT pg_stat_statements_reset()"); err != nil {
				fmt.Fprintf(os.Stderr, "reset statement stats: %v\n", err)
			}
			r.startSessionMetrics()
		case *events.HistorySync:
			r.historySyncs.Add(1)
			conversations := event.Data.GetConversations()
			r.historyConvs.Add(int64(len(conversations)))
			for _, conversation := range conversations {
				r.historyMessages.Add(int64(len(conversation.GetMessages())))
			}
		case *events.Message:
			if event.Message.GetConversation() != "ping" {
				return
			}
			r.startOnce.Do(r.startMetrics)
			r.received.Add(1)
			jobs := r.jobs[jobShard(event.Info.Chat, len(r.jobs))]
			select {
			case jobs <- event:
			default:
				r.overflows.Add(1)
				r.failed.Add(1)
				r.checkDone()
			}
		}
	}
}

func (r *runner) startSessionMetrics() {
	r.metricsMu.Lock()
	if !r.sessionStarted {
		r.sessionStart = runtimeSnapshot()
		r.sessionStarted = true
	}
	r.metricsMu.Unlock()
}

func (r *runner) startMetrics() {
	r.metricsMu.Lock()
	r.runtimeStart = runtimeSnapshot()
	r.resourceStart = resourceSnapshot()
	r.metricsStarted = true
	r.tempStop = make(chan struct{})
	r.metricsMu.Unlock()
	go r.sampleTemporaryFiles()
	r.startedAt.Store(time.Now().UnixNano())
}

func (r *runner) sampleTemporaryFiles() {
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		bytes, files := tempDirUsage()
		updatePeak(&r.tempPeakBytes, bytes)
		updatePeak(&r.tempPeakFiles, files)
		select {
		case <-r.tempStop:
			return
		case <-ticker.C:
		}
	}
}

func updatePeak(target *atomic.Int64, value int64) {
	for previous := target.Load(); value > previous && !target.CompareAndSwap(previous, value); previous = target.Load() {
	}
}

func (r *runner) scanQR(code string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.BarbackURL+"/admin/mock-phone/scan-qr", bytes.NewBufferString(code))
	if err != nil {
		fmt.Fprintf(os.Stderr, "create QR scan request: %v\n", err)
		return
	}
	req.Header.Set("Content-Type", "text/plain")
	resp, err := r.httpClient.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "submit QR scan: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		fmt.Fprintf(os.Stderr, "submit QR scan: %s: %s\n", resp.Status, strings.TrimSpace(string(body)))
	}
}

func jobShard(jid types.JID, count int) int {
	const offset64 = uint64(14695981039346656037)
	const prime64 = uint64(1099511628211)
	hash := offset64
	for i := range jid.User {
		hash = (hash ^ uint64(jid.User[i])) * prime64
	}
	for i := range jid.Server {
		hash = (hash ^ uint64(jid.Server[i])) * prime64
	}
	hash = (hash ^ uint64(jid.Device)) * prime64
	return int(hash % uint64(count))
}

func (r *runner) sendWorker(ctx context.Context, client *whatsmeow.Client, jobs <-chan *events.Message) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-jobs:
			sequence := r.messageSequence.Add(1) - 1
			outgoing, category, mediaBytes, uploadDuration, err := buildWorkloadMessage(ctx, client, string(msg.Info.ID), sequence, r.cfg.Workload.MessageProfile)
			if uploadDuration > 0 {
				r.mediaUploads.Add(1)
				r.mediaBytes.Add(mediaBytes)
				r.latencyMu.Lock()
				r.uploadLatencies = append(r.uploadLatencies, float64(uploadDuration)/float64(time.Millisecond))
				r.latencyMu.Unlock()
			}
			if err == nil {
				r.latencyMu.Lock()
				r.messageTypes[category]++
				r.latencyMu.Unlock()
			}
			started := time.Now()
			if err == nil {
				_, err = client.SendMessage(ctx, msg.Info.Chat, outgoing)
			}
			latency := float64(time.Since(started)) / float64(time.Millisecond)
			r.latencyMu.Lock()
			r.latencies = append(r.latencies, latency)
			r.latencyMu.Unlock()
			if err != nil {
				r.failed.Add(1)
				r.recordFailure(err)
				fmt.Fprintf(os.Stderr, "send pong %s: %v\n", msg.Info.ID, err)
			} else {
				r.sent.Add(1)
			}
			r.checkDone()
		}
	}
}

func (r *runner) checkDone() {
	if r.sent.Load()+r.failed.Load() >= r.cfg.Total {
		r.doneOnce.Do(func() {
			r.finishedAt.Store(time.Now().UnixNano())
			close(r.done)
		})
	}
}

func (r *runner) snapshot(completed bool) result {
	now := time.Now()
	startedAt := r.startedAt.Load()
	finishedAt := r.finishedAt.Load()
	startedTime := time.Time{}
	duration := time.Duration(0)
	if startedAt != 0 {
		startedTime = time.Unix(0, startedAt)
		if finishedAt != 0 {
			duration = time.Duration(finishedAt - startedAt)
		} else {
			duration = now.Sub(startedTime)
		}
	}
	runtimeNow := runtimeSnapshot()
	res := result{
		Variant:              r.cfg.Variant,
		Revision:             revision,
		StartedAt:            startedTime,
		Completed:            completed && r.failed.Load() == 0 && r.sent.Load() == r.cfg.Total,
		TargetMessages:       r.cfg.Total,
		Workload:             r.cfg.Workload,
		MessagesReceived:     r.received.Load(),
		MessagesSent:         r.sent.Load(),
		SendFailures:         r.failed.Load(),
		FailureReasons:       r.failureReasonSnapshot(),
		QueueOverflows:       r.overflows.Load(),
		HistorySyncs:         r.historySyncs.Load(),
		HistoryConversations: r.historyConvs.Load(),
		HistoryMessages:      r.historyMessages.Load(),
		DurationMS:           float64(duration) / float64(time.Millisecond),
		SendLatency:          r.latencySnapshot(),
		Runtime:              runtimeNow,
		MessageTypes:         r.messageTypeSnapshot(),
		MediaUploads:         r.mediaUploads.Load(),
		MediaUploadBytes:     r.mediaBytes.Load(),
		MediaUploadLatency:   r.uploadLatencySnapshot(),
		BusinessAppValidated: r.businessValid.Load(),
	}
	r.metricsMu.Lock()
	if r.sessionStarted {
		res.SessionRuntime = runtimeDelta(runtimeNow, r.sessionStart)
	}
	if r.metricsStarted {
		res.WorkloadRuntime = runtimeDelta(runtimeNow, r.runtimeStart)
		res.Resources = resourceDelta(resourceSnapshot(), r.resourceStart)
		finalTempBytes, finalTempFiles := tempDirUsage()
		res.Resources.Temp = tempIOStats{
			PeakBytes:  r.tempPeakBytes.Load(),
			PeakFiles:  r.tempPeakFiles.Load(),
			FinalBytes: finalTempBytes,
			FinalFiles: finalTempFiles,
		}
	}
	r.metricsMu.Unlock()
	if duration > 0 {
		res.ThroughputPerSec = float64(res.MessagesSent) / duration.Seconds()
	}
	if r.db != nil {
		res.Database = databaseSnapshot(r.db)
	}
	return res
}

func (r *runner) latencySnapshot() latencyStats {
	r.latencyMu.Lock()
	values := append([]float64(nil), r.latencies...)
	r.latencyMu.Unlock()
	return summarizeLatencies(values)
}

func (r *runner) uploadLatencySnapshot() latencyStats {
	r.latencyMu.Lock()
	values := append([]float64(nil), r.uploadLatencies...)
	r.latencyMu.Unlock()
	return summarizeLatencies(values)
}

func (r *runner) messageTypeSnapshot() map[string]int64 {
	r.latencyMu.Lock()
	defer r.latencyMu.Unlock()
	result := make(map[string]int64, len(r.messageTypes))
	for key, value := range r.messageTypes {
		result[key] = value
	}
	return result
}

func (r *runner) recordFailure(err error) {
	r.latencyMu.Lock()
	reason := err.Error()
	if _, exists := r.failureReasons[reason]; exists || len(r.failureReasons) < 16 {
		r.failureReasons[reason]++
	} else {
		r.failureReasons["other"]++
	}
	r.latencyMu.Unlock()
}

func (r *runner) failureReasonSnapshot() map[string]int64 {
	r.latencyMu.Lock()
	defer r.latencyMu.Unlock()
	if len(r.failureReasons) == 0 {
		return nil
	}
	result := make(map[string]int64, len(r.failureReasons))
	for key, value := range r.failureReasons {
		result[key] = value
	}
	return result
}

func summarizeLatencies(values []float64) latencyStats {
	if len(values) == 0 {
		return latencyStats{}
	}
	sort.Float64s(values)
	return latencyStats{
		MinMS: values[0],
		P50MS: percentile(values, 0.50),
		P95MS: percentile(values, 0.95),
		P99MS: percentile(values, 0.99),
		MaxMS: values[len(values)-1],
	}
}

func percentile(values []float64, p float64) float64 {
	index := int(float64(len(values)-1) * p)
	return values[index]
}

func databaseSnapshot(db *sql.DB) databaseStats {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	stats := databaseStats{}
	_ = db.QueryRowContext(ctx, "SELECT pg_database_size(current_database())").Scan(&stats.SizeBytes)
	_ = db.QueryRowContext(ctx, `
		SELECT COALESCE(SUM(calls), 0), COALESCE(SUM(total_exec_time), 0), COALESCE(SUM(rows), 0)
		FROM pg_stat_statements
		WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		  AND query ILIKE '%whatsmeow_%'`).Scan(&stats.Calls, &stats.TotalExecMS, &stats.Rows)
	rows, err := db.QueryContext(ctx, `
		SELECT LEFT(query, 512), calls, total_exec_time, rows
		FROM pg_stat_statements
		WHERE dbid = (SELECT oid FROM pg_database WHERE datname = current_database())
		  AND query ILIKE '%whatsmeow_%'
		ORDER BY calls DESC, total_exec_time DESC
		LIMIT 30`)
	if err != nil {
		return stats
	}
	defer rows.Close()
	for rows.Next() {
		var item queryStat
		if err = rows.Scan(&item.Query, &item.Calls, &item.TotalExecMS, &item.Rows); err != nil {
			break
		}
		item.Query = strings.Join(strings.Fields(item.Query), " ")
		stats.Queries = append(stats.Queries, item)
	}
	return stats
}

func runtimeSnapshot() runtimeStats {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	var usage syscall.Rusage
	_ = syscall.Getrusage(syscall.RUSAGE_SELF, &usage)
	return runtimeStats{
		AllocBytes:      mem.Alloc,
		HeapInuseBytes:  mem.HeapInuse,
		SysBytes:        mem.Sys,
		TotalAllocBytes: mem.TotalAlloc,
		NumGC:           mem.NumGC,
		Goroutines:      runtime.NumGoroutine(),
		PeakRSSBytes:    peakRSS(),
		UserCPUSeconds:  timevalSeconds(usage.Utime),
		SysCPUSeconds:   timevalSeconds(usage.Stime),
	}
}

func peakRSS() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmHWM:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, _ := strconv.ParseInt(fields[1], 10, 64)
		return kb * 1024
	}
	return 0
}

func timevalSeconds(value syscall.Timeval) float64 {
	return float64(value.Sec) + float64(value.Usec)/1e6
}

func barbackRootKey() [32]byte {
	return [32]byte{
		0x70, 0x5e, 0x3c, 0x5b, 0x8d, 0xe0, 0x52, 0xe7,
		0xb6, 0x46, 0xff, 0xd5, 0x69, 0xfe, 0x6f, 0x79,
		0x84, 0xd3, 0xf2, 0x4b, 0x45, 0xed, 0x4b, 0x3a,
		0x21, 0xff, 0x71, 0x5d, 0x30, 0x3e, 0xcf, 0x73,
	}
}

func trustedHTTPClient(caPath, serverName string) (*http.Client, error) {
	pem, err := os.ReadFile(caPath)
	if err != nil {
		return nil, fmt.Errorf("read Barback TLS CA: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("parse Barback TLS CA")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{RootCAs: roots, ServerName: serverName, MinVersion: tls.VersionTLS12}
	return &http.Client{Transport: transport}, nil
}

func writeResult(path string, value result) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err = os.MkdirAll(filepath.Dir(path), 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
