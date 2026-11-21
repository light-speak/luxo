package rpc

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/api"
	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestClientCancellationReachesRemotePreparation(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "unary"
		if streaming {
			name = "stream-before-ack"
		}
		t.Run(name, func(t *testing.T) {
			router := api.NewRouter()
			if err := router.Schema.RegisterAPI(&schema.API{ID: 1, Name: "wait", Stream: streaming, ReturnType: "Int"}); err != nil {
				t.Fatal(err)
			}
			if err := router.Registry.RegisterSchemaAPIs(router.Schema); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			stopped := make(chan error, 1)
			release := make(chan struct{})
			defer close(release)
			wait := func(ctx context.Context) error {
				close(started)
				select {
				case <-ctx.Done():
					stopped <- ctx.Err()
					return ctx.Err()
				case <-release:
					return errors.New("test cleanup")
				}
			}
			if streaming {
				router.HandleStreamPrepared("wait", func(ctx context.Context, _ *api.Request, _ any) (api.PreparedStreamHandler, error) {
					return nil, wait(ctx)
				})
			} else {
				router.Handle("wait", func(ctx context.Context, _ *api.Request) error { return wait(ctx) })
			}
			client := NewClient(startTestRPCServer(t, NewServer(router)))
			defer client.Close()
			deadline, stop := context.WithTimeout(t.Context(), 3*time.Second)
			defer stop()
			request, cancel := context.WithCancel(deadline)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				if streaming {
					sub, err := client.SubscribeContext(request, "", 1, nil, nil)
					if sub != nil {
						sub.Close()
						err = errors.New("unexpected subscription")
					}
					result <- err
				} else {
					_, err := client.CallWithMaskContext(request, "", 1, nil, nil)
					result <- err
				}
			}()
			select {
			case <-started:
			case <-deadline.Done():
				t.Fatal("remote did not start")
			}
			cancel()
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("client cancellation", err)
				}
			case <-deadline.Done():
				t.Fatal("client did not stop")
			}
			select {
			case err := <-stopped:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("remote cancellation", err)
				}
			case <-deadline.Done():
				t.Fatal("remote did not observe client cancellation")
			}
			if router.Streams.SubCount("wait") != 0 {
				t.Fatal("preparation retained subscription")
			}
		})
	}
}
