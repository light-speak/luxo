package codegen

import (
	"fmt"
	"strings"

	"github.com/light-speak/luxo/pkg/semantic"
)

// EntryBackends is an immutable build selection, not runtime configuration.
// Empty or "none" disables an optional component. Used events/cache require
// an explicit backend; connection addresses never choose an adapter.
type EntryBackends struct {
	Events string
	Queue  string
	Cache  string
}

// Validate rejects unsupported selections before any entry is emitted.
func (selection EntryBackends) Validate(result *semantic.Result) error {
	if result == nil {
		return fmt.Errorf("codegen: backend selection requires a semantic result")
	}
	for _, component := range []struct{ name, value, remote string }{
		{"events", selection.Events, "nats"}, {"queue", selection.Queue, "nats"}, {"cache", selection.Cache, "redis"},
	} {
		switch component.value {
		case "", "none", "memory", component.remote:
		default:
			return fmt.Errorf("codegen: unsupported %s backend %q", component.name, component.value)
		}
	}
	modules := collectModules(result)
	events, _ := entryCapabilities(modules)
	if events && !backendEnabled(selection.Events) {
		return fmt.Errorf("codegen: events/streams require an explicit memory or nats build backend")
	}
	if modulesNeedCache(modules) && !backendEnabled(selection.Cache) {
		return fmt.Errorf("codegen: @cache requires an explicit memory or redis build backend")
	}
	return nil
}

func writeCacheBackendImport(b *strings.Builder, backend string) {
	path := "github.com/light-speak/luxo/pkg/lux/cache"
	if backend == "redis" {
		path += "/redis"
	}
	fmt.Fprintf(b, "\t%q\n", path)
}

func backendEnabled(value string) bool { return value != "" && value != "none" }

func entryBackendSelection(options []EntryBackends) (EntryBackends, error) {
	if len(options) > 1 {
		return EntryBackends{}, fmt.Errorf("codegen: exactly one backend selection is allowed")
	}
	if len(options) == 1 {
		return options[0], nil
	}
	return EntryBackends{}, nil
}

func writeBackendGuard(b *strings.Builder, name, selection string) {
	if !backendEnabled(selection) {
		selection = "none"
	}
	writeStartupRegistration(b, "backend selection", fmt.Sprintf("env.CheckBackend(%q, %q)", name, selection))
}

func writeSelectedEventBus(b *strings.Builder, selection string) {
	writeBackendGuard(b, "LUXO_EVENT_BACKEND", selection)
	if selection == "memory" {
		b.WriteString("\teventBus := event.NewChanBus(256)\n\tdefer eventBus.Close()\n\n")
		return
	}
	writeRequiredNATSURL(b, "eventURL")
	writeBackendInitialization(b, "eventBus", "event.NewNATSBus(eventURL)")
}

func writeSelectedQueue(b *strings.Builder, selection string) {
	writeBackendGuard(b, "LUXO_QUEUE_BACKEND", selection)
	if !backendEnabled(selection) {
		return
	}
	if selection == "memory" {
		b.WriteString("\ttaskQueue := queue.NewMemoryQueue(queue.DefaultConfig())\n\tdefer taskQueue.Close()\n\n")
		return
	}
	writeRequiredNATSURL(b, "queueURL")
	writeBackendInitialization(b, "taskQueue", "natsqueue.NewNATSQueue(queueURL, queue.DefaultConfig())")
}

func writeRequiredNATSURL(b *strings.Builder, variable string) {
	fmt.Fprintf(b, "\t%s, err := env.Required(\"NATS_URL\")\n", variable)
	b.WriteString("\tif err != nil { fmt.Fprintln(os.Stderr, err); os.Exit(1) }\n")
}

func writeEventBackendImport(b *strings.Builder, backend string) {
	path := "github.com/light-speak/luxo/pkg/lux/event"
	if backend == "nats" {
		path = "github.com/light-speak/luxo/pkg/lux/nats/event"
	}
	fmt.Fprintf(b, "\tevent %q\n", path)
}

func writeQueueBackendImports(b *strings.Builder, backend string) {
	if !backendEnabled(backend) {
		return
	}
	b.WriteString("\t\"github.com/light-speak/luxo/pkg/lux/queue\"\n")
	if backend == "nats" {
		b.WriteString("\tnatsqueue \"github.com/light-speak/luxo/pkg/lux/nats/queue\"\n")
	}
}
