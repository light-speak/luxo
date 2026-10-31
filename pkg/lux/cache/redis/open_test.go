package redis

import (
	"context"
	"crypto/rand"
	"errors"
	"github.com/containerd/errdefs"
	"github.com/light-speak/luxo/pkg/cachepolicy"
	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/schema"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"net"
	"testing"
	"time"
)

type failedCacheContainerStartup struct{ err error }

func (failure failedCacheContainerStartup) WaitUntilReady(context.Context, wait.StrategyTarget) error {
	return failure.err
}

func TestResponseCacheContainerFailureStillCleansUp(t *testing.T) {
	startupErr := errors.New("test: injected readiness failure")
	var created testcontainers.Container
	t.Run("startup-failure", func(t *testing.T) {
		container, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{Image: "redis:7.4-alpine", WaitingFor: failedCacheContainerStartup{startupErr}}, Started: true,
		})
		testcontainers.CleanupContainer(t, container)
		if container == nil || !errors.Is(err, startupErr) {
			t.Fatalf("expected created container plus startup failure: %v", err)
		}
		if _, err := container.Inspect(t.Context()); err != nil {
			t.Fatalf("fixture must still exist before cleanup: %v", err)
		}
		created = container
	})
	if created == nil {
		t.Fatal("startup failure did not exercise a created container")
	}
	// The parent process and its Ryuk connection are still alive. A missing
	// container proves the subtest cleanup, not eventual process-exit reaping.
	_, err := created.Inspect(t.Context())
	if !errdefs.IsNotFound(err) {
		t.Fatalf("startup failure leaked a container: %v", err)
	}
}

func TestOpenRejectsMissingOrUnavailableRedis(t *testing.T) {
	for _, address := range []string{"", "not-an-address", "127.0.0.1:1"} {
		t.Setenv("REDIS_URL", address)
		if backend, err := Open(t.Context()); err == nil || backend != nil {
			t.Fatal("unavailable Redis accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if backend, err := Open(ctx); !errors.Is(err, context.Canceled) || backend != nil {
		t.Fatal("cancellation ignored")
	}
}

func TestResponseCacheRedisEnvironmentReadiness(t *testing.T) {
	container, err := testcontainers.GenericContainer(t.Context(), testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{Image: "redis:7.4-alpine", ExposedPorts: []string{"6379/tcp"}, WaitingFor: wait.ForListeningPort("6379/tcp")}, Started: true,
	})
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatal(err)
	}
	host, err := container.Host(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(t.Context(), "6379/tcp")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("REDIS_URL", net.JoinHostPort(host, port.Port()))
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_PREFIX", rand.Text())
	for name, value := range map[string]string{"LUXO_CACHE_PROJECT": "project", "LUXO_CACHE_ENVIRONMENT": "test", "LUXO_CACHE_REVISION": "revision", "LUXO_CACHE_ORIGIN_CONCURRENCY": "16", "LUXO_CACHE_ORIGIN_MAX_WAITERS": "0", "LUXO_CACHE_ORIGIN_WAIT_TIMEOUT": "0s"} {
		t.Setenv(name, value)
	}
	for _, service := range []string{"first", "second"} {
		binding, err := api.NewResponseCacheFromEnv(t.Context(), service, Open)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = binding.Close() })
		assertRedisBindingIsolation(t, binding, service)
	}
}

func assertRedisBindingIsolation(t *testing.T, binding *api.ResponseCache, want string) {
	t.Helper()
	router := api.NewRouterWithOptions(api.RouterOptions{ResponseCache: binding})
	if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "same", ReturnType: "String", CachePolicy: &schema.CachePolicy{Mode: cachepolicy.TTL, TTL: time.Minute, Scope: cachepolicy.Private}, CacheDependencies: &schema.CacheDependencies{}}); err != nil {
		t.Fatal(err)
	}
	router.RequireCache("same")
	calls := 0
	router.Handle("same", func(ctx context.Context, req *api.Request) error {
		return api.CacheResponsePolicy(ctx, req, "identity", func() error { calls++; req.Buf.B = append(req.Buf.B, want...); return nil })
	})
	if err := router.Prepare(); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		req := &api.Request{API: "same", Buf: &api.ResponseBuf{}}
		if err := router.ExportHandlers()["same"](t.Context(), req); err != nil || string(req.Buf.B) != want {
			t.Fatalf("isolation: %q %v", req.Buf.B, err)
		}
	}
	if calls != 1 {
		t.Fatalf("cache did not hit: %d", calls)
	}
}
