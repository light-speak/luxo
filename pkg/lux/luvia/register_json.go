package luvia

import (
	"time"

	"github.com/light-speak/luxo/pkg/lux"
)

func (gr *GatewayRegistrar) registrationHeader(w *observationJSON, gatewayAPI, serviceAPI string) {
	operation := gatewayAPI
	if gr.nodeType == "service" {
		operation = serviceAPI
	}
	w.text(`{"$api":`, operation)
	w.text(`,"apiKey":`, gr.apiKey)
	w.text(`,"projectId":`, gr.projectID)
	w.text(`,"instanceId":`, gr.instanceID)
}

func (gr *GatewayRegistrar) encodeRegistration() ([]byte, error) {
	var w observationJSON
	gr.registrationHeader(&w, "registerGateway", "registerServiceNode")
	w.text(`,"$select":`, "id")
	w.text(`,"name":`, gr.nodeName)
	if gr.nodeType == "service" {
		w.text(`,"addr":`, gr.endpoint)
		w.text(`,"version":`, gr.version)
	} else {
		w.text(`,"endpoint":`, gr.endpoint)
		if gr.introKey == "" {
			w.buf.AppendString(`,"introKey":null`)
		} else {
			w.text(`,"introKey":`, gr.introKey)
		}
	}
	return w.finish("}")
}

func (gr *GatewayRegistrar) encodeDeregistration() ([]byte, error) {
	var w observationJSON
	gr.registrationHeader(&w, "deregisterGateway", "deregisterServiceNode")
	return w.finish("}")
}

func (gr *GatewayRegistrar) encodeHeartbeat(memoryMB, cpuPercent float64, uptime time.Duration, dependencies []lux.RuntimeDependencyStats) ([]byte, error) {
	var w observationJSON
	if capacity := heartbeatJSONCapacity(dependencies); capacity > 0 {
		w.buf.B = make([]byte, 0, capacity)
	}
	gr.registrationHeader(&w, "heartbeat", "heartbeatServiceNode")
	w.text(`,"version":`, gr.version)
	w.integer(`,"uptime":`, int64(uptime))
	w.number(`,"memoryMB":`, memoryMB)
	w.number(`,"cpuPercent":`, cpuPercent)
	if dependencies == nil {
		w.buf.AppendString(`,"dependencies":null`)
	} else {
		w.buf.AppendString(`,"dependencies":[`)
		for i := range dependencies {
			if i > 0 {
				w.buf.AppendString(",")
			}
			appendDependencyStats(&w, &dependencies[i])
		}
		w.buf.AppendString("]")
	}
	return w.finish("}")
}

const maxHeartbeatJSONReserve = 1 << 20

// Size hint only: escaped strings and wide numbers may grow beyond it. Keep
// eager allocation bounded without rescanning text or duplicating JSON rules.
func heartbeatJSONCapacity(dependencies []lux.RuntimeDependencyStats) int {
	if len(dependencies) == 0 || len(dependencies) > (maxHeartbeatJSONReserve-256)/256 {
		return 0
	}
	size := 256 + 256*len(dependencies)
	for i := range dependencies {
		d := &dependencies[i]
		for _, text := range [...]string{d.Key, string(d.Kind), d.Provider, d.Target, d.Name, string(d.Status)} {
			if len(text) > maxHeartbeatJSONReserve-size {
				return 0
			}
			size += len(text)
		}
	}
	return size
}

func appendDependencyStats(w *observationJSON, d *lux.RuntimeDependencyStats) {
	w.text(`{"key":`, d.Key)
	w.text(`,"kind":`, string(d.Kind))
	w.text(`,"provider":`, d.Provider)
	w.text(`,"target":`, d.Target)
	w.text(`,"name":`, d.Name)
	w.text(`,"status":`, string(d.Status))
	if d.LatencyMs == nil {
		w.buf.AppendString(`,"latencyMs":null`)
	} else {
		w.number(`,"latencyMs":`, *d.LatencyMs)
	}
	w.integer(`,"poolMax":`, d.PoolMax)
	w.integer(`,"poolTotal":`, d.PoolTotal)
	w.integer(`,"poolInUse":`, d.PoolInUse)
	w.integer(`,"poolIdle":`, d.PoolIdle)
	w.integer(`,"poolConstructing":`, d.PoolConstructing)
	w.integer(`,"poolAcquireCount":`, d.PoolAcquireCount)
	w.number(`,"poolAcquireMs":`, d.PoolAcquireMs)
	w.integer(`,"poolEmptyAcquireCount":`, d.PoolEmptyAcquireCount)
	w.integer(`,"poolCanceledCount":`, d.PoolCanceledCount)
	w.buf.AppendString("}")
}
