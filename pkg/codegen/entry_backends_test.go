package codegen

import (
	"strings"
	"testing"

	"github.com/light-speak/luxo/pkg/ast"
	"github.com/light-speak/luxo/pkg/semantic"
)

func TestEntryBackendSelection(t *testing.T) {
	input := &semantic.Result{Files: []*ast.File{{Name: "origin/service.luxo", Events: []*ast.EventDecl{{Name: "Changed"}}}}}
	if _, err := GenerateEntryFileChecked(input, "example.com/app", EntryBackends{}); err == nil {
		t.Fatal("event use without a build backend accepted")
	}
	for _, backend := range []string{"memory", "nats"} {
		source, err := GenerateEntryFileChecked(input, "example.com/app", EntryBackends{Events: backend})
		if err != nil {
			t.Fatal(err)
		}
		code := string(source)
		if strings.Contains(code, "event.NewFromEnv") || strings.Contains(code, "queue.NewFromEnv") || strings.Contains(code, "taskQueue") {
			t.Fatalf("implicit backend or unused queue: %s", code)
		}
		constructor := "event.NewChanBus(256)"
		if backend == "nats" {
			constructor = "event.NewNATSBus("
		}
		if !strings.Contains(code, constructor) {
			t.Fatalf("missing static %s", constructor)
		}
		if got := strings.Contains(code, `"github.com/light-speak/luxo/pkg/lux/nats/event"`); got != (backend == "nats") {
			t.Fatalf("NATS import does not match build selection %q", backend)
		}
		for _, unused := range []string{"pkg/lux/cache/redis", "pkg/lux/storage/s3", "pkg/lux/nats/queue"} {
			if strings.Contains(code, unused) {
				t.Fatalf("unused adapter imported: %s", unused)
			}
		}
	}
}

func TestInvalidEntryBackendSelection(t *testing.T) {
	for _, selection := range []EntryBackends{{Events: "redis"}, {Queue: "redis"}, {Cache: "nats"}} {
		if err := selection.Validate(&semantic.Result{}); err == nil {
			t.Fatalf("accepted %+v", selection)
		}
	}
}

func TestCheckedEntriesRejectBackendConfiguration(t *testing.T) {
	input := &semantic.Result{}
	for _, options := range [][]EntryBackends{{{Events: "unsupported"}}, {{}, {}}} {
		if source, err := GenerateEntryFileChecked(input, "example.com/app", options...); err == nil || source != nil {
			t.Fatal("embedded entry accepted invalid backend configuration")
		}
		if sources, err := GenerateModuleEntryFilesChecked(input, "example.com/app", options...); err == nil || sources != nil {
			t.Fatal("module entries accepted invalid backend configuration")
		}
		if source, err := GenerateGatewayEntryChecked(input, "example.com/app", options...); err == nil || source != nil {
			t.Fatal("gateway entry accepted invalid backend configuration")
		}
	}
	if err := (EntryBackends{}).Validate(nil); err == nil {
		t.Fatal("backend validation accepted missing semantic result")
	}
}

func TestRemoteCacheAndQueueEntrySelection(t *testing.T) {
	input := analyzeCachedFixture(t, []*ast.File{{Name: "origin/service.luxo", APIs: []*ast.ApiDecl{cachedTestAPI(true)}}}, true)
	if len(input.Errors) != 0 {
		t.Fatal(input.Errors)
	}
	if err := (EntryBackends{Cache: "none"}).Validate(input); err == nil {
		t.Fatal("cached API accepted a disabled backend")
	}
	source, err := GenerateEntryFileChecked(input, "example.com/app", EntryBackends{Queue: "nats", Cache: "redis"})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		`"github.com/light-speak/luxo/pkg/lux/cache/redis"`,
		`"github.com/light-speak/luxo/pkg/lux/nats/queue"`,
		`env.CheckBackend("LUXO_CACHE_BACKEND", "redis")`,
		`env.CheckBackend("LUXO_QUEUE_BACKEND", "nats")`,
		`api.NewResponseCacheFromEnv(ctx, "embedded", redis.Open)`,
		`natsqueue.NewNATSQueue(queueURL, queue.DefaultConfig())`,
	} {
		if !strings.Contains(string(source), required) {
			t.Fatalf("selected adapter missing %q", required)
		}
	}
	if strings.Contains(string(source), "pkg/lux/nats/event") || strings.Contains(string(source), "cache.OpenMemory") {
		t.Fatal("remote queue/cache selection imported an unselected adapter")
	}
}
