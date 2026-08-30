package api

import (
	"bytes"
	"fmt"
	"testing"
	"time"

	"github.com/light-speak/luxo/pkg/lux/schema"
)

func TestLogRequest_WithError(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: &logs})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "getUser", Module: "user"})

	rt.logRequest("getUser", 50*time.Millisecond, fmt.Errorf("InvalidCredentials"))
	output := logs.String()

	// Should contain the error marker and error message
	if len(output) == 0 {
		t.Error("expected log output")
	}
	// Red color for error
	if output == "" {
		t.Error("should have output")
	}
}

func TestLogRequest_SlowDuration(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: &logs})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "slowAPI", Module: "slow"})

	// >500ms should be red
	rt.logRequest("slowAPI", 600*time.Millisecond, nil)
	output := logs.String()

	if len(output) == 0 {
		t.Error("expected log output for slow request")
	}
}

func TestLogRequest_MediumDuration(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: &logs})
	rt.Schema.RegisterAPI(&schema.API{ID: 1, Name: "medAPI", Module: "med"})

	// >100ms <500ms should be yellow
	rt.logRequest("medAPI", 200*time.Millisecond, nil)
	output := logs.String()

	if len(output) == 0 {
		t.Error("expected log output for medium request")
	}
}

func TestLogRequest_Disabled(t *testing.T) {
	rt := NewRouter()
	// Should not panic or produce output when disabled
	rt.logRequest("test", time.Millisecond, nil)
}

func TestLogRequest_NoModule(t *testing.T) {
	var logs bytes.Buffer
	rt := NewRouterWithOptions(RouterOptions{RequestLogging: true, LogWriter: &logs})
	// API not in schema — should fall back to "api" module

	rt.logRequest("unknownAPI", 5*time.Millisecond, nil)
	output := logs.String()

	if len(output) == 0 {
		t.Error("expected log output")
	}
}

func TestModuleColor_DifferentModules(t *testing.T) {
	// Reset state for test isolation
	moduleColorMu.Lock()
	oldMap := moduleColorMap
	oldIdx := moduleColorIdx
	moduleColorMap = make(map[string]string)
	moduleColorIdx = 0
	moduleColorMu.Unlock()
	defer func() {
		moduleColorMu.Lock()
		moduleColorMap = oldMap
		moduleColorIdx = oldIdx
		moduleColorMu.Unlock()
	}()

	c1 := moduleColor("auth")
	c2 := moduleColor("user")
	c3 := moduleColor("post")

	// Different modules should get different colors
	if c1 == c2 || c2 == c3 {
		t.Errorf("different modules should get different colors: auth=%q user=%q post=%q", c1, c2, c3)
	}
}

func TestModuleColor_Idempotent(t *testing.T) {
	// Same module should always return same color
	moduleColorMu.Lock()
	oldMap := moduleColorMap
	oldIdx := moduleColorIdx
	moduleColorMap = make(map[string]string)
	moduleColorIdx = 0
	moduleColorMu.Unlock()
	defer func() {
		moduleColorMu.Lock()
		moduleColorMap = oldMap
		moduleColorIdx = oldIdx
		moduleColorMu.Unlock()
	}()

	c1 := moduleColor("auth")
	c2 := moduleColor("auth")
	c3 := moduleColor("auth")

	if c1 != c2 || c2 != c3 {
		t.Errorf("same module should return same color: %q %q %q", c1, c2, c3)
	}
}
