package main

import (
	"context"
	"encoding/base64"
	"errors"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestLoadConfigWorkload(t *testing.T) {
	t.Setenv("BENCH_TOTAL", "960")
	t.Setenv("BENCH_TIMEOUT", "2m")
	t.Setenv("BENCH_SCENARIO", "dm-parallel-32")
	t.Setenv("BENCH_MODE", "dm")
	t.Setenv("BENCH_RATE", "120")
	t.Setenv("BENCH_SENDERS", "32")
	t.Setenv("BENCH_WORKERS", "4")
	t.Setenv("BENCH_WARMUP_MS", "2500")
	t.Setenv("BENCH_GROUP_SIZE", "0")
	t.Setenv("HISTORY_CONVERSATIONS", "10")
	t.Setenv("HISTORY_MESSAGES", "5")
	t.Setenv("BENCH_BUSINESS_SMOKE", "1")
	t.Setenv("BENCH_PHONE_CONSENT_SMOKE", "1")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Total != 960 || cfg.Workload.Scenario != "dm-parallel-32" || cfg.Workload.Mode != "dm" {
		t.Fatalf("unexpected config: %+v", cfg)
	}
	if cfg.Workload.Rate != 120 || cfg.Workload.Senders != 32 || cfg.Workload.Workers != 4 || cfg.Workload.WarmupMS != 2500 {
		t.Fatalf("unexpected workload: %+v", cfg.Workload)
	}
	if cfg.Workload.HistoryConversations != 10 || cfg.Workload.HistoryMessages != 5 {
		t.Fatalf("unexpected history workload: %+v", cfg.Workload)
	}
	if !cfg.BusinessSmoke {
		t.Fatal("business smoke validation was not enabled")
	}
	if !cfg.PhoneConsentSmoke {
		t.Fatal("phone consent smoke validation was not enabled")
	}
}

func TestLoadConfigRejectsInvalidWorkload(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "BENCH_RATE", value: "0"},
		{name: "BENCH_SENDERS", value: "-1"},
		{name: "BENCH_WORKERS", value: "0"},
		{name: "BENCH_WARMUP_MS", value: "-1"},
		{name: "HISTORY_CONVERSATIONS", value: "bad"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("BENCH_MODE", "dm")
			t.Setenv(test.name, test.value)
			if _, err := loadConfig(); err == nil {
				t.Fatalf("expected %s=%q to fail", test.name, test.value)
			}
		})
	}
}

func TestLoadConfigRejectsInvalidMode(t *testing.T) {
	t.Setenv("BENCH_MODE", "broadcast")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected invalid benchmark mode to fail")
	}
}

func TestRunnerReportsFatalSmokeFailure(t *testing.T) {
	r := &runner{fatalErr: make(chan error, 1)}
	sentinel := errors.New("phone consent failed")
	r.reportFatal(sentinel)
	select {
	case err := <-r.fatalErr:
		if !errors.Is(err, sentinel) {
			t.Fatalf("fatal error = %v", err)
		}
	default:
		t.Fatal("fatal smoke failure was not propagated")
	}
}

func TestWaitForRunCompletionWakesOnFatalSmokeFailure(t *testing.T) {
	r := &runner{done: make(chan struct{}), fatalErr: make(chan error, 1)}
	sentinel := errors.New("phone consent failed")
	r.reportFatal(sentinel)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := r.waitForRunCompletion(ctx); !errors.Is(err, sentinel) {
		t.Fatalf("completion error = %v, want %v", err, sentinel)
	}
}

func TestPhoneConsentFailureIsSharedAcrossWorkers(t *testing.T) {
	r := &runner{
		fatalErr:       make(chan error, 1),
		failureReasons: make(map[string]int64),
	}
	sentinel := errors.New("phone consent failed")
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int64
	validate := func() error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-release
		return sentinel
	}

	var workers sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errs <- r.runPhoneConsentSmoke(validate)
		}()
	}
	<-entered
	close(release)
	workers.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, sentinel) {
			t.Fatalf("worker error = %v, want %v", err, sentinel)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("validation calls = %d, want 1", calls.Load())
	}
}

func TestPhoneConsentSmokeRunsBeforeMeasuredMetrics(t *testing.T) {
	r := &runner{
		cfg:            config{PhoneConsentSmoke: true},
		fatalErr:       make(chan error, 1),
		failureReasons: make(map[string]int64),
	}
	if !r.beforeBenchmarkMessage(func() error {
		if r.startedAt.Load() != 0 {
			t.Fatal("workload metrics started before phone consent validation")
		}
		return nil
	}) {
		t.Fatal("phone consent validation failed")
	}
	r.startOnce.Do(r.startMetrics)
	t.Cleanup(r.stopMetricsSampler)
	if r.startedAt.Load() == 0 {
		t.Fatal("workload metrics did not start after phone consent validation")
	}
}

func TestPhoneConsentFailureDoesNotStartMeasuredMetrics(t *testing.T) {
	r := &runner{
		cfg:            config{PhoneConsentSmoke: true},
		fatalErr:       make(chan error, 1),
		failureReasons: make(map[string]int64),
	}
	if r.beforeBenchmarkMessage(func() error { return errors.New("failed") }) {
		t.Fatal("failed phone consent validation allowed the workload to start")
	}
	if r.startedAt.Load() != 0 {
		t.Fatal("failed phone consent validation started workload metrics")
	}
	if r.failed.Load() != 0 {
		t.Fatalf("phone consent validation changed workload send failures to %d", r.failed.Load())
	}
}

func TestPhoneConsentResetsDatabaseBeforeMeasuredMetrics(t *testing.T) {
	jobs := make(chan *events.Message, 1)
	var resetCalls atomic.Int64
	r := &runner{
		cfg:            config{PhoneConsentSmoke: true},
		fatalErr:       make(chan error, 1),
		failureReasons: make(map[string]int64),
		jobs:           []chan *events.Message{jobs},
	}
	r.phoneConsentValidator = func(context.Context, *whatsmeow.Client, types.JID) error {
		if resetCalls.Load() != 0 {
			t.Fatal("statement stats reset before phone consent validation")
		}
		return nil
	}
	r.statementStatsReset = func(context.Context) error {
		if r.startedAt.Load() != 0 {
			t.Fatal("statement stats reset after workload metrics started")
		}
		resetCalls.Add(1)
		return nil
	}
	r.handler(context.Background(), &whatsmeow.Client{})(&events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("100000011111111", types.HiddenUserServer)}},
		Message: &waE2E.Message{Conversation: proto.String("ping")},
	})
	t.Cleanup(r.stopMetricsSampler)
	if resetCalls.Load() != 1 {
		t.Fatalf("statement stats reset calls = %d, want 1", resetCalls.Load())
	}
	if r.startedAt.Load() == 0 {
		t.Fatal("workload metrics did not start after statement stats reset")
	}
}

func TestHandlerUsesRunContextForPhoneConsent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &runner{
		cfg:            config{PhoneConsentSmoke: true},
		fatalErr:       make(chan error, 1),
		failureReasons: make(map[string]int64),
		phoneConsentValidator: func(ctx context.Context, _ *whatsmeow.Client, _ types.JID) error {
			return ctx.Err()
		},
	}
	r.handler(ctx, &whatsmeow.Client{})(&events.Message{
		Info:    types.MessageInfo{MessageSource: types.MessageSource{Chat: types.NewJID("100000011111111", types.HiddenUserServer)}},
		Message: &waE2E.Message{Conversation: proto.String("ping")},
	})
	select {
	case err := <-r.fatalErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("phone consent error = %v, want context canceled", err)
		}
	default:
		t.Fatal("canceled run context was not propagated to phone consent validation")
	}
}

func TestLoadConfigRejectsInvalidMessageProfile(t *testing.T) {
	t.Setenv("BENCH_MESSAGE_PROFILE", "production")
	if _, err := loadConfig(); err == nil {
		t.Fatal("expected invalid benchmark message profile to fail")
	}
}

func TestSnapshotUsesMessageCompletionBoundary(t *testing.T) {
	startedAt := time.Now().Add(-10 * time.Second)
	r := &runner{cfg: config{Total: 1}}
	r.startedAt.Store(startedAt.UnixNano())
	r.finishedAt.Store(startedAt.Add(8 * time.Second).UnixNano())
	r.sent.Store(1)

	result := r.snapshot(true)
	if result.DurationMS != 8000 {
		t.Fatalf("duration includes post-run settling time: got %.0fms", result.DurationMS)
	}
	if result.ThroughputPerSec != 0.125 {
		t.Fatalf("unexpected throughput: %f", result.ThroughputPerSec)
	}
}

func TestJobShardKeepsChatOrdered(t *testing.T) {
	chat := types.NewJID("15551234567", types.DefaultUserServer)
	first := jobShard(chat, 64)
	for range 100 {
		if got := jobShard(chat, 64); got != first {
			t.Fatalf("chat moved shards: got %d, want %d", got, first)
		}
	}
}

func TestJobShardDistributesChats(t *testing.T) {
	seen := make(map[int]struct{})
	for i := range 64 {
		chat := types.NewJID("1555123"+strconv.Itoa(i), types.DefaultUserServer)
		seen[jobShard(chat, 64)] = struct{}{}
	}
	if len(seen) < 32 {
		t.Fatalf("chat sharding is too concentrated: %d shards", len(seen))
	}
}

func TestContainsPhoneNumberConsentCaptures(t *testing.T) {
	requestPayload, err := proto.Marshal(buildRequestPhoneNumberMessage())
	if err != nil {
		t.Fatal(err)
	}
	sharePayload, err := proto.Marshal(buildSharePhoneNumberMessage())
	if err != nil {
		t.Fatal(err)
	}
	captures := []capturedMessage{{PlaintextBase64: base64.StdEncoding.EncodeToString(requestPayload)}}
	if containsPhoneNumberConsentCaptures(captures) {
		t.Fatal("request-only capture passed phone consent validation")
	}
	captures = append(captures, capturedMessage{PlaintextBase64: base64.StdEncoding.EncodeToString(sharePayload)})
	if !containsPhoneNumberConsentCaptures(captures) {
		t.Fatal("request and share messages were not detected")
	}
}

func TestPhoneConsentTargetPrefersSenderLID(t *testing.T) {
	message := &events.Message{Info: types.MessageInfo{MessageSource: types.MessageSource{
		Chat:      types.NewJID("15550001111", types.DefaultUserServer),
		Sender:    types.NewJID("15550001111", types.DefaultUserServer),
		SenderAlt: types.NewJID("100000011111111", types.HiddenUserServer),
	}}}
	if got := phoneConsentTarget(message); got.String() != "100000011111111@lid" {
		t.Fatalf("target = %s", got)
	}
}
