package luvia

import (
	"encoding/json"
	"math"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux"
)

func TestStaticRegistrationJSON(t *testing.T) {
	for _, kind := range []string{"gateway", "service"} {
		gr := &GatewayRegistrar{nodeType: kind, apiKey: "key\"", projectID: "project", instanceID: "instance", nodeName: "node", endpoint: "localhost:9000", version: "beta"}
		body, err := gr.encodeRegistration()
		if err != nil {
			t.Fatal(err)
		}
		var got map[string]json.RawMessage
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if kind == "gateway" {
			if string(got["introKey"]) != "null" || string(got["$api"]) != `"registerGateway"` {
				t.Fatalf("body=%s", body)
			}
		} else {
			if _, ok := got["introKey"]; ok {
				t.Fatal("service includes introKey")
			}
			if string(got["addr"]) != `"localhost:9000"` || string(got["$api"]) != `"registerServiceNode"` {
				t.Fatalf("body=%s", body)
			}
		}
	}
}

func TestStaticHeartbeatPreservesInt64(t *testing.T) {
	gr := &GatewayRegistrar{nodeType: "service"}
	body, err := gr.encodeHeartbeat(1.5, 2.5, 123*time.Nanosecond, []lux.RuntimeDependencyStats{{PoolAcquireCount: math.MaxInt64}})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Uptime       json.Number `json:"uptime"`
		Dependencies []struct {
			Count   json.Number     `json:"poolAcquireCount"`
			Latency json.RawMessage `json:"latencyMs"`
		} `json:"dependencies"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.Uptime != "123" || got.Dependencies[0].Count != "9223372036854775807" || string(got.Dependencies[0].Latency) != "null" {
		t.Fatalf("body=%s", body)
	}
}

func TestStaticRegistrarRejectsInvalidFields(t *testing.T) {
	gr := &GatewayRegistrar{apiKey: string([]byte{255})}
	if body, err := gr.encodeRegistration(); err == nil || body != nil {
		t.Fatal("invalid registration accepted")
	}
	if body, err := gr.encodeDeregistration(); err == nil || body != nil {
		t.Fatal("invalid deregistration accepted")
	}
	gr.apiKey = "valid"
	latency := math.NaN()
	if body, err := gr.encodeHeartbeat(0, 0, 0, []lux.RuntimeDependencyStats{{LatencyMs: &latency}}); err == nil || body != nil {
		t.Fatal("non-finite latency accepted")
	}
}

// Matches the previous map/struct boundary, with the control route name aligned.
func previousHeartbeatJSON(gr *GatewayRegistrar, dependencies []lux.RuntimeDependencyStats) ([]byte, error) {
	operation := "heartbeat"
	if gr.nodeType == "service" {
		operation = "heartbeatServiceNode"
	}
	return json.Marshal(map[string]any{"$api": operation, "apiKey": gr.apiKey, "projectId": gr.projectID,
		"instanceId": gr.instanceID, "version": gr.version, "uptime": int64(123),
		"memoryMB": 1.5, "cpuPercent": 2.5, "dependencies": dependencies})
}

func heartbeatEncodingFixture(count int) []lux.RuntimeDependencyStats {
	dependencies := make([]lux.RuntimeDependencyStats, count)
	latency := 1.5
	for i := range dependencies {
		dependencies[i] = lux.RuntimeDependencyStats{Key: strconv.Itoa(i), Provider: "postgresql", Name: "业务库",
			Target: "localhost:5432", LatencyMs: &latency, PoolMax: 20, PoolTotal: 3, PoolIdle: 2, PoolInUse: 1,
			PoolAcquireCount: 42, PoolAcquireMs: 0.3, PoolEmptyAcquireCount: 2, PoolCanceledCount: 1, PoolConstructing: 1}
	}
	return dependencies
}

func TestHeartbeatEncoderBaselineParity(t *testing.T) {
	for _, kind := range []string{"gateway", "service"} {
		gr := &GatewayRegistrar{nodeType: kind, apiKey: "key", projectID: "project", instanceID: "node", version: "beta.1"}
		for _, dependencies := range [][]lux.RuntimeDependencyStats{nil, {}, heartbeatEncodingFixture(2)} {
			previous, err := previousHeartbeatJSON(gr, dependencies)
			if err != nil {
				t.Fatal(err)
			}
			current, err := gr.encodeHeartbeat(1.5, 2.5, 123, dependencies)
			if err != nil {
				t.Fatal(err)
			}
			var before, after map[string]any
			if err := json.Unmarshal(previous, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(current, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("previous=%s current=%s", previous, current)
			}
		}
	}
}

func BenchmarkHeartbeatEncodingComparison(b *testing.B) {
	gr := &GatewayRegistrar{nodeType: "service", apiKey: "key", projectID: "project", instanceID: "node", version: "beta.1"}
	for _, count := range []int{0, 4, 128} {
		dependencies := heartbeatEncodingFixture(count)
		b.Run(strconv.Itoa(count)+"/previous", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := previousHeartbeatJSON(gr, dependencies); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run(strconv.Itoa(count)+"/current", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := gr.encodeHeartbeat(1.5, 2.5, 123, dependencies); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func TestHeartbeatJSONCapacity(t *testing.T) {
	if got := heartbeatJSONCapacity(nil); got != 0 {
		t.Fatal(got)
	}
	if got := heartbeatJSONCapacity(heartbeatEncodingFixture(4)); got <= 0 || got > maxHeartbeatJSONReserve {
		t.Fatal(got)
	}
	if got := heartbeatJSONCapacity(make([]lux.RuntimeDependencyStats, maxHeartbeatJSONReserve/256)); got != 0 {
		t.Fatal(got)
	}
	if got := heartbeatJSONCapacity([]lux.RuntimeDependencyStats{{Name: strings.Repeat("x", maxHeartbeatJSONReserve)}}); got != 0 {
		t.Fatal(got)
	}
	gr := &GatewayRegistrar{}
	for _, name := range []string{strings.Repeat("\"\n", 2048), string([]byte{255})} {
		body, err := gr.encodeHeartbeat(0, 0, 0, []lux.RuntimeDependencyStats{{Name: name}})
		if name == string([]byte{255}) {
			if err == nil || body != nil {
				t.Fatal("invalid text accepted")
			}
			continue
		}
		if err != nil || !json.Valid(body) {
			t.Fatal(err)
		}
	}
}
