package main

import (
	"bytes"
	"context"
	"crypto/sha256"
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
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.mau.fi/libsignal/ecc"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var revision = "working-tree"

type config struct {
	DatabaseURL   string
	BarbackURL    string
	BarbackWS     string
	TLSCAPath     string
	TLSServerName string
	OutputPath    string
	Variant       string
	Total         int64
	Timeout       time.Duration
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
	Variant              string        `json:"variant"`
	Revision             string        `json:"revision"`
	StartedAt            time.Time     `json:"started_at"`
	Completed            bool          `json:"completed"`
	Error                string        `json:"error,omitempty"`
	TargetMessages       int64         `json:"target_messages"`
	MessagesReceived     int64         `json:"messages_received"`
	MessagesSent         int64         `json:"messages_sent"`
	SendFailures         int64         `json:"send_failures"`
	QueueOverflows       int64         `json:"queue_overflows"`
	HistorySyncs         int64         `json:"history_syncs"`
	HistoryConversations int64         `json:"history_conversations"`
	HistoryMessages      int64         `json:"history_messages"`
	DurationMS           float64       `json:"duration_ms"`
	ThroughputPerSec     float64       `json:"throughput_per_sec"`
	SendLatency          latencyStats  `json:"send_latency"`
	Database             databaseStats `json:"database"`
	Runtime              runtimeStats  `json:"runtime"`
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

	startOnce sync.Once
	doneOnce  sync.Once
	start     time.Time
	done      chan struct{}
	jobs      chan *events.Message

	latencyMu sync.Mutex
	latencies []float64
}

func main() {
	cfg, err := loadConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	r := &runner{
		cfg:       cfg,
		done:      make(chan struct{}),
		jobs:      make(chan *events.Message, 4096),
		latencies: make([]float64, 0, cfg.Total),
	}
	res, runErr := r.run()
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
	return config{
		DatabaseURL:   env("DATABASE_URL", "postgres://postgres:postgres@postgres:5432/hypermeow?sslmode=disable"),
		BarbackURL:    env("BARBACK_URL", "http://barback:8080"),
		BarbackWS:     env("BARBACK_WS", "ws://barback:8080/ws/chat"),
		TLSCAPath:     os.Getenv("BARBACK_TLS_CA"),
		TLSServerName: env("BARBACK_TLS_SERVER_NAME", "0.0.0.0"),
		OutputPath:    env("RESULT_PATH", "/results/result.json"),
		Variant:       env("BENCH_VARIANT", "candidate"),
		Total:         total,
		Timeout:       timeout,
	}, nil
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
	go r.sendWorker(workerCtx, client)

	if err = client.ConnectContext(ctx); err != nil {
		return r.snapshot(false), fmt.Errorf("connect: %w", err)
	}
	defer client.Disconnect()

	select {
	case <-r.done:
		time.Sleep(2 * time.Second)
		res := r.snapshot(true)
		return res, nil
	case <-ctx.Done():
		return r.snapshot(false), fmt.Errorf("benchmark incomplete: %w", ctx.Err())
	}
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
			if _, err := r.db.ExecContext(context.Background(), "SELECT pg_stat_statements_reset()"); err != nil {
				fmt.Fprintf(os.Stderr, "reset statement stats: %v\n", err)
			}
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
			r.startOnce.Do(func() { r.start = time.Now() })
			r.received.Add(1)
			select {
			case r.jobs <- event:
			default:
				r.overflows.Add(1)
				r.failed.Add(1)
				r.checkDone()
			}
		}
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

func (r *runner) sendWorker(ctx context.Context, client *whatsmeow.Client) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-r.jobs:
			started := time.Now()
			_, err := client.SendMessage(ctx, msg.Info.Chat, &waE2E.Message{
				Conversation: proto.String("pong " + string(msg.Info.ID)),
			})
			latency := float64(time.Since(started)) / float64(time.Millisecond)
			r.latencyMu.Lock()
			r.latencies = append(r.latencies, latency)
			r.latencyMu.Unlock()
			if err != nil {
				r.failed.Add(1)
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
		r.doneOnce.Do(func() { close(r.done) })
	}
}

func (r *runner) snapshot(completed bool) result {
	now := time.Now()
	duration := time.Duration(0)
	if !r.start.IsZero() {
		duration = now.Sub(r.start)
	}
	res := result{
		Variant:              r.cfg.Variant,
		Revision:             revision,
		StartedAt:            r.start,
		Completed:            completed && r.failed.Load() == 0 && r.sent.Load() == r.cfg.Total,
		TargetMessages:       r.cfg.Total,
		MessagesReceived:     r.received.Load(),
		MessagesSent:         r.sent.Load(),
		SendFailures:         r.failed.Load(),
		QueueOverflows:       r.overflows.Load(),
		HistorySyncs:         r.historySyncs.Load(),
		HistoryConversations: r.historyConvs.Load(),
		HistoryMessages:      r.historyMessages.Load(),
		DurationMS:           float64(duration) / float64(time.Millisecond),
		SendLatency:          r.latencySnapshot(),
		Runtime:              runtimeSnapshot(),
	}
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
	seed := sha256.Sum256([]byte("mock_root_key"))
	return ecc.CreateKeyPair(seed[:]).PublicKey().PublicKey()
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
