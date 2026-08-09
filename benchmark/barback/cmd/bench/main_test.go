package main

import (
	"encoding/base64"
	"errors"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"go.mau.fi/whatsmeow"
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
	requestPayload, err := proto.Marshal(whatsmeow.BuildRequestPhoneNumberMessage(nil))
	if err != nil {
		t.Fatal(err)
	}
	sharePayload, err := proto.Marshal(whatsmeow.BuildSharePhoneNumberMessage())
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
