package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func TestIsolationProfileCommand(t *testing.T) {
	for _, tc := range []struct{ pkg, want string }{
		{"pkg/lux/api", "api-non-identity\t.\napi-local-route-export\t^BenchmarkLocalRouteExport$\n" +
			"api-canonical-json\t^BenchmarkCanonicalJSON(Structured|Typed|Bytes|Float|Value|Integer)$\n" +
			"api-binary-param-lookup\t^BenchmarkBinaryParamLookup$\n" +
			"api-request-log\t^BenchmarkRequestLogBoundary$|^BenchmarkRequestLogDelivered$\n"},
		{"pkg/lux/luvia", "luvia-primary-trace\t^BenchmarkPrimaryKeys$|^BenchmarkTraceEncodingComparison$\n" +
			"luvia-gateway-identity\t^BenchmarkGatewayIdentity$\n" +
			"luvia-auth-middleware\t^BenchmarkAuthMiddlewareIsolation$\n" +
			"luvia-merge-destination\t^BenchmarkMergeDestination$\n" +
			"luvia-resolved-fields\t^BenchmarkValidateResolvedFields$\n" +
			"luvia-convenience-keys\t^BenchmarkConvenienceKeyParentValidation$\n"},
		{"pkg/codegen", "codegen-integer-literals\t^BenchmarkCompileDecimalIntegerLiterals$\n" +
			"codegen-global-receiver\t^BenchmarkGlobalReceiverName$\n" +
			"codegen-migration-state\t^BenchmarkGlobalReceiverName$|^BenchmarkMigrationStateValidation$\n"},
		{"pkg/lux", "lux-short-path\t^BenchmarkStrictFilterContract$|^BenchmarkDecimalAlignment$\n"},
		{"pkg/lux/rpc", "rpc-owned-request\t^BenchmarkLRPCOwnedRequest$\n" +
			"rpc-deadline-classification\t^BenchmarkRPCDeadlineClassification$\n" +
			"rpc-nil-context-guard\t^BenchmarkRPCNilContextGuard$\n" +
			"rpc-prepared-frame\t^BenchmarkLRPCPreparedFrame$\n"},
		{"pkg/lux/queue", "queue-stream-name\t^BenchmarkQueueStreamName$\n"},
	} {
		var output bytes.Buffer
		if err := run([]string{"isolation-profile", tc.pkg}, nil, &output, io.Discard); err != nil || output.String() != tc.want {
			t.Fatalf("discovery profile for %s = %q, %v", tc.pkg, &output, err)
		}
	}
	if err := run([]string{"isolation-profile", benchlayout.CodegenPackage}, nil, closedIsolationWriter{}, io.Discard); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("isolation output failure = %v", err)
	}
}

func TestConvenienceKeyProfileRejectsMissingFixture(t *testing.T) {
	root := newBenchmarkRunTree(t)
	for _, fixture := range []string{"convenience_keys_bench_test.go", "primary_keys_bench_test.go"} {
		writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuviaPackage, fixture), "package luvia")
	}
	_, _, err := compileOverlay(root, benchlayout.LuviaPackage, false, "luvia-convenience-keys")
	if err == nil || !strings.Contains(err.Error(), "missing Luvia convenience keys test fixture") {
		t.Fatalf("missing primary-key baseline fixture returned %v", err)
	}
	if !strings.Contains(performanceProfile+"\n", "\nluvia-convenience-keys-overlay-v1\n") {
		t.Fatal("performance compile profile lacks a convenience fixture binding")
	}
}

type closedIsolationWriter struct{}

func (closedIsolationWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestLinkedOverlaysPreserveFixtureBoundaries(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pkg, profile string
		assert       func(*testing.T, map[string]string, string)
	}{
		{benchlayout.APIPackage, "api-local-route-export", func(t *testing.T, replace map[string]string, dir string) {
			verifyOnlyListedTestFiles(t, replace, dir, "local_input_bench_test.go")
		}},
		{benchlayout.APIPackage, "api-canonical-json", verifyAPICanonicalJSONOverlay},
		{benchlayout.APIPackage, "api-binary-param-lookup", func(t *testing.T, replace map[string]string, dir string) {
			verifyOnlyListedTestFiles(t, replace, dir, "binary_param_lookup_test.go")
		}},
		{"pkg/lux/rpc", "rpc-owned-request", verifyRPCOwnedRequestOverlay},
		{"pkg/lux/rpc", "rpc-deadline-classification", verifyRPCDeadlineOverlay},
		{"pkg/lux/rpc", "rpc-nil-context-guard", verifyRPCNilContextGuardOverlay},
		{"pkg/lux/rpc", "rpc-prepared-frame", func(t *testing.T, replace map[string]string, dir string) {
			verifyNativePairedFixture(t, replace, dir, "frame_prepared_bench_test.go", "8f4ca28038d34633c5ef5b4379d2621a51272237aaf249b9e4d769538db2053e")
		}},
		{"pkg/lux/nats/queue", "queue-stream-name", func(t *testing.T, replace map[string]string, dir string) {
			verifyNativePairedFixture(t, replace, dir, "nats_retry_bench_test.go", "0f0a5195bb203151b16c1ec768d139153e822c29c7143da8193de9369ac8d8e6")
		}},
		{benchlayout.APIPackage, benchlayout.APINonIdentityProfile, verifyAPIOverlay},
		{benchlayout.APIPackage, "api-request-log", verifyAPIRequestLogOverlay},
		{benchlayout.LuxPackage, benchlayout.LuxShortPathProfile, verifyLuxShortPathOverlay},
		{benchlayout.LuviaPackage, benchlayout.LuviaPrimaryTraceProfile, verifyLuviaOverlay},
		{benchlayout.LuviaPackage, benchlayout.LuviaGatewayIdentityProfile, verifyLuviaGatewayIdentityOverlay},
		{benchlayout.LuviaPackage, "luvia-auth-middleware", verifyLuviaAuthMiddlewareOverlay},
		{benchlayout.LuviaPackage, "luvia-merge-destination", verifyLuviaMergeDestinationOverlay},
		{benchlayout.LuviaPackage, "luvia-convenience-keys", func(t *testing.T, replace map[string]string, dir string) {
			verifyOnlyListedTestFiles(t, replace, dir, "convenience_keys_bench_test.go",
				"primary_keys_bench_test.go", "primary_keys_baseline_test.go")
		}},
		{benchlayout.LuviaPackage, "luvia-resolved-fields", func(t *testing.T, replace map[string]string, dir string) {
			verifyOnlyListedTestFiles(t, replace, dir, "grouped_response_bench_test.go", "grouped_response_test.go",
				"merger_bench_test.go", "primary_keys_bench_test.go", "primary_keys_baseline_test.go")
		}},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			file, cleanup, err := compileOverlay(root, tc.pkg, false, tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := cleanup(); err != nil {
					t.Error(err)
				}
			}()
			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			var overlay struct{ Replace map[string]string }
			if err := json.Unmarshal(data, &overlay); err != nil {
				t.Fatal(err)
			}
			tc.assert(t, overlay.Replace, filepath.Join(root, tc.pkg))
		})
	}
}

func verifyNativePairedFixture(t *testing.T, replace map[string]string, dir, fixture, witness string) {
	t.Helper()
	path := filepath.Join(dir, fixture)
	projected, err := os.ReadFile(replace[path])
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(projected)); got != witness {
		t.Fatalf("projection hash = %s, want native witness %s", got, witness)
	}
	delete(replace, path)
	verifyOnlyListedTestFiles(t, replace, dir, fixture)
}

func TestQueueStreamNameRequiresItsPhysicalAdapter(t *testing.T) {
	root := newBenchmarkRunTree(t)
	writeBenchmarkRunFixture(t, root, "pkg/lux/queue/ordinary_test.go", "package queue")
	for _, command := range []string{"compile-performance", "compile-performance-baseline", "run-performance"} {
		err := run([]string{command, root, filepath.Join(root, "bin"), "pkg/lux/queue", "queue-stream-name"}, nil, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "missing benchmark package pkg/lux/nats/queue") {
			t.Fatalf("%s fell back to the memory queue: %v", command, err)
		}
	}
}

func TestPairedFixtureOverlaysRejectInvalidInputs(t *testing.T) {
	for _, fixture := range []struct{ pkg, name, profile string }{
		{"pkg/lux/rpc", "frame_prepared_bench_test.go", "rpc-prepared-frame"},
		{"pkg/lux/nats/queue", "nats_retry_bench_test.go", "queue-stream-name"},
	} {
		t.Run(fixture.profile, func(t *testing.T) {
			body, err := os.ReadFile(filepath.Join("../..", fixture.pkg, fixture.name))
			if err != nil {
				t.Fatal(err)
			}
			root := newBenchmarkRunTree(t)
			other := filepath.Join(fixture.pkg, "ordinary_test.go")
			path := filepath.Join(fixture.pkg, fixture.name)
			writeBenchmarkRunFixture(t, root, other, "package sample\n")
			checkFailure := func(message string) {
				t.Helper()
				_, cleanup, err := compileOverlay(root, fixture.pkg, true, fixture.profile)
				if cleanup != nil || err == nil || !strings.Contains(err.Error(), message) {
					t.Fatalf("wanted %q and no surviving workspace, got %v", message, err)
				}
			}
			checkFailure("missing")
			writeBenchmarkRunFixture(t, root, path, "// edited fixture\n"+string(body))
			checkFailure("does not match the verified source")
			writeBenchmarkRunFixture(t, root, path, string(body))
			writeBenchmarkRunFixture(t, root, other, "package sample\nfunc TestMain() {}\n")
			checkFailure("cannot isolate test package setup")
			writeBenchmarkRunFixture(t, root, other, "package sample\n")
			nonregular := filepath.Join(root, fixture.pkg, "nonregular_test.go")
			if err := os.Mkdir(nonregular, 0o700); err != nil {
				t.Fatal(err)
			}
			checkFailure("fixture must be a regular file")
		})
	}
}

func TestPairedFixtureRejectsInvalidWitness(t *testing.T) {
	root := newBenchmarkRunTree(t)
	const name = "bench_test.go"
	identity := pairedBenchmarkFixture{name: name}
	for _, tc := range []struct{ body, want string }{
		{"package", "expected 'IDENT'"},
		{"package sample\nfunc BenchmarkFixture() {}\n", "does not match the verified projection"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			writeBenchmarkRunFixture(t, root, name, tc.body)
			identity.sourceSHA = fmt.Sprintf("%x", sha256.Sum256([]byte(tc.body)))
			path, err := identity.projectFile(filepath.Join(root, name), root)
			if path != "" || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid signed fixture returned %q, %v", path, err)
			}
		})
	}
}

func verifyLuviaAuthMiddlewareOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	const fixture = "middleware_bench_test.go"
	const helper = "middleware_test.go"
	for _, tc := range []struct{ path, want string }{
		{filepath.Join(dir, fixture), "a5a5cd5191827423e07bf382d572614207c3e7fb5b7df27452963c3136bb2d8e"},
		{replace[filepath.Join(dir, helper)], "c70f9f8da6377c484739423793d637304f974bb0e1f1ebfd10fa75eb8d6eb3f3"},
	} {
		body, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(body)); got != tc.want {
			t.Fatalf("%s hash = %s, want native witness %s", tc.path, got, tc.want)
		}
	}
	delete(replace, filepath.Join(dir, helper))
	verifyOnlyListedTestFiles(t, replace, dir, fixture, helper)
}

func TestLuviaAuthMiddlewareDefaultKeepsOrdinaryTestInputs(t *testing.T) {
	root := newBenchmarkRunTree(t)
	dir := filepath.Join(benchlayout.LuviaPackage, "ordinary_test.go")
	writeBenchmarkRunFixture(t, root, dir, "package luvia")
	for _, baseline := range []bool{false, true} {
		overlay, cleanup, err := compileOverlay(root, benchlayout.LuviaPackage, baseline, benchlayout.DefaultProfile)
		if err != nil || overlay != "" {
			t.Fatalf("ordinary Luvia overlay = %q, %v", overlay, err)
		}
		if err := cleanup(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLuviaAuthMiddlewareOverlayRejectsNonregularFixture(t *testing.T) {
	root := newBenchmarkRunTree(t)
	dir := filepath.Join(root, benchlayout.LuviaPackage)
	if _, err := writeLuviaAuthMiddlewareOverlay(dir, root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing package returned %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "nonregular_test.go"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLuviaAuthMiddlewareOverlay(dir, root); err == nil || !strings.Contains(err.Error(), "must be a regular file") {
		t.Fatalf("nonregular fixture returned %v", err)
	}
}

func TestLuviaAuthMiddlewareOverlayReportsUnreadableFixtures(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read fixtures without permission bits")
	}
	for _, fixture := range []string{luviaAuthBenchmarkFixture, luviaAuthHelperFixture} {
		t.Run(fixture, func(t *testing.T) {
			root := newBenchmarkRunTree(t)
			relative := filepath.Join(benchlayout.LuviaPackage, fixture)
			writeBenchmarkRunFixture(t, root, relative, "package luvia")
			filename := filepath.Join(root, relative)
			if err := os.Chmod(filename, 0); err != nil {
				t.Fatal(err)
			}
			_, cleanup, err := compileOverlay(root, benchlayout.LuviaPackage, false, benchlayout.LuviaAuthMiddlewareProfile)
			if cleanup != nil {
				defer func() {
					if err := cleanup(); err != nil {
						t.Error(err)
					}
				}()
			}
			if !errors.Is(err, os.ErrPermission) {
				t.Fatalf("unreadable fixture returned %v, want permission error", err)
			}
		})
	}
}

func TestLuviaAuthMiddlewareOverlayRejectsChangedWorkload(t *testing.T) {
	const pkg = "pkg/lux/luvia"
	const profile = "luvia-auth-middleware"
	const benchmark = "middleware_bench_test.go"
	const helper = "middleware_test.go"
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(performanceProfile+"\n", "\nluvia-auth-middleware-overlay-v1 "+benchmark+"=") {
		t.Fatal("performance compile profile lacks the Luvia auth fixture binding")
	}
	for _, tc := range []struct{ name, filename, before, after, message string }{
		{"missing benchmark", benchmark, "", "", "missing Luvia auth middleware test fixture"},
		{"changed benchmark", benchmark, "middlewareTokenSink", "changedSink", "does not match the verified benchmark"},
		{"missing helper", helper, "", "", "missing Luvia auth middleware test fixture"},
		{"changed helper", helper, "time.Hour", "time.Minute", "does not match the verified projection"},
		{"missing helper boundary", helper, "func testIdentity(", "func alternateIdentity(", "missing or repeated helper declarations"},
		{"repeated helper", helper, "func testCfg()", "func testCfg() {}\nfunc testCfg()", "missing or repeated helper declarations"},
		{"unrelated test changes", helper, "func TestJWTHeaderBoundaryHTTPWebSocketAndInternal", "func TestRenamedHeaderBoundary", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scratch := newBenchmarkRunTree(t)
			for _, name := range []string{benchmark, helper} {
				body, err := os.ReadFile(filepath.Join(root, pkg, name))
				if err != nil {
					t.Fatal(err)
				}
				source := string(body)
				if name == tc.filename {
					if tc.before == "" {
						continue
					}
					source = strings.Replace(source, tc.before, tc.after, 1)
				}
				writeBenchmarkRunFixture(t, scratch, filepath.Join(pkg, name), source)
			}
			_, cleanup, err := compileOverlay(scratch, pkg, false, profile)
			if err == nil {
				defer func() {
					if err := cleanup(); err != nil {
						t.Error(err)
					}
				}()
			}
			if tc.message == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("auth middleware fixture error = %v, want %s", err, tc.message)
			}
		})
	}
}

func verifyRPCDeadlineOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	for name, expected := range map[string]string{
		"stream_termination_bench_test.go": "d73d6805aa1aa0c5a0212d3cb246adc17ce92a116c4f31f83cf10b32ce531efb",
		"stream_termination_test.go":       "237955931ab9e5c7791eed9965a0262c8e0902d0e0db328d5c0ec31b84ed446a",
	} {
		input := filepath.Join(dir, name)
		projected, err := os.ReadFile(replace[input])
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(projected)); got != expected {
			t.Fatalf("%s projected hash = %s, want native witness %s", name, got, expected)
		}
		delete(replace, input)
	}
	verifyOnlyListedTestFiles(t, replace, dir, "stream_termination_bench_test.go", "stream_termination_test.go")
}

func TestRPCDeadlineOverlayRejectsChangedProjection(t *testing.T) {
	const pkg = "pkg/lux/rpc"
	const profile = "rpc-deadline-classification"
	const benchmark = "stream_termination_bench_test.go"
	const context = "stream_termination_test.go"
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(performanceProfile+"\n", "\nrpc-deadline-classification-overlay-v1 "+benchmark+"=") {
		t.Fatal("performance compile profile lacks the RPC deadline projection binding")
	}
	for _, tc := range []struct{ name, filename, before, after, message string }{
		{"missing benchmark", benchmark, "var deadlineClassificationSink error", "var alternateSink error", "missing or repeated fixture start"},
		{"duplicate benchmark", benchmark, "func BenchmarkRPCDeadlineClassification(b *testing.B)", "func BenchmarkRPCDeadlineClassification(b *testing.B) {}\nfunc BenchmarkRPCDeadlineClassification(b *testing.B)", "missing or repeated fixture end"},
		{"workload changed", benchmark, "time.Hour", "time.Minute", "does not match the verified projection"},
		{"missing context boundary", context, "pendingDeadlineContext) Deadline()", "pendingDeadlineContext) OtherDeadline()", "missing or repeated fixture end"},
		{"missing context receiver", context, "func (c pendingDeadlineContext) Deadline()", "func Deadline()", "missing or repeated fixture end"},
		{"context changed", context, "return c.deadline, true", "return c.deadline, false", "does not match the verified projection"},
		{"different package", context, "package rpc", "package rpc_test", "requires package rpc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scratch := newBenchmarkRunTree(t)
			for _, name := range []string{benchmark, context} {
				body, err := os.ReadFile(filepath.Join(root, pkg, name))
				if err != nil {
					t.Fatal(err)
				}
				source := string(body)
				if name == tc.filename {
					source = strings.Replace(source, tc.before, tc.after, 1)
				}
				writeBenchmarkRunFixture(t, scratch, filepath.Join(pkg, name), source)
			}
			_, _, err := compileOverlay(scratch, pkg, false, profile)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("deadline fixture error = %v, want %s", err, tc.message)
			}
		})
	}
}

func verifyRPCNilContextGuardOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	const fixture = "auth_context_bench_test.go"
	const witness = "cd1b4a5dd0d0622ce19cfc144057482a1059cd61245cf38cf9205f46aaece2c3"
	path := filepath.Join(dir, fixture)
	projected, err := os.ReadFile(replace[path])
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(projected)); got != witness {
		t.Fatalf("nil-context projection hash = %s, want native witness %s", got, witness)
	}
	delete(replace, path)
	verifyOnlyListedTestFiles(t, replace, dir, fixture)
}

func TestRPCNilContextGuardOverlayGuardsProjectionBoundaries(t *testing.T) {
	const pkg, profile = "pkg/lux/rpc", "rpc-nil-context-guard"
	const fixture, rootFn = "auth_context_bench_test.go", "func BenchmarkRPCNilContextGuard(b *testing.B)"
	data, err := os.ReadFile(filepath.Join("../..", pkg, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(performanceProfile+"\n", "\nrpc-nil-context-guard-overlay-v1 "+fixture+"=") {
		t.Fatal("performance compile profile lacks the RPC nil-context projection binding")
	}
	for _, tc := range []struct{ name, before, after, message string }{
		{"missing", "", "", "missing RPC nil-context guard fixture"},
		{"different package", "package rpc", "package rpc_test", "requires package rpc"},
		{"missing root", rootFn, "func BenchmarkOther(b *testing.B)", "missing or repeated fixture end"},
		{"duplicate root", rootFn, rootFn + " {}\n" + rootFn, "missing or repeated fixture end"},
		{"changed workload", "invalid token", "invalid credentials", "does not match the verified projection"},
		{"independent benchmark renamed", "BenchmarkRPCAuthenticationErrorRecovery", "BenchmarkRPCAuthErrorRecovery", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newBenchmarkRunTree(t)
			writeBenchmarkRunFixture(t, root, filepath.Join(pkg, "other_test.go"), "package rpc")
			if tc.before != "" {
				writeBenchmarkRunFixture(t, root, filepath.Join(pkg, fixture), strings.Replace(string(data), tc.before, tc.after, 1))
			}
			_, cleanup, err := compileOverlay(root, pkg, tc.message == "", profile)
			if tc.message != "" {
				if err == nil || !strings.Contains(err.Error(), tc.message) {
					t.Fatalf("nil-context fixture error = %v, want %s", err, tc.message)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := cleanup(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRPCNilContextGuardOverlayRejectsNonregularFixture(t *testing.T) {
	const pkg = "pkg/lux/rpc"
	root := newBenchmarkRunTree(t)
	writeBenchmarkRunFixture(t, root, filepath.Join(pkg, "other_test.go"), "package rpc")
	if err := os.Symlink("missing.go", filepath.Join(root, pkg, "auth_context_bench_test.go")); err != nil {
		t.Fatal(err)
	}
	_, _, err := compileOverlay(root, pkg, false, "rpc-nil-context-guard")
	if err == nil || !strings.Contains(err.Error(), "fixture must be a regular file") {
		t.Fatalf("nonregular nil-context fixture returned %v", err)
	}
}

func TestRPCDeadlineOverlayRequiresBothRegularFixtures(t *testing.T) {
	const pkg = "pkg/lux/rpc"
	root := newBenchmarkRunTree(t)
	dir := filepath.Join(root, pkg)
	if _, err := writeRPCDeadlineOverlay(dir, root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing RPC directory returned %v", err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(pkg, "unrelated_test.go"), "package rpc")
	if _, err := writeRPCDeadlineOverlay(dir, root); err == nil || !strings.Contains(err.Error(), "missing RPC deadline fixture") {
		t.Fatalf("missing deadline projection returned %v", err)
	}
	if err := os.Symlink("missing.go", filepath.Join(dir, "stream_termination_test.go")); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRPCDeadlineOverlay(dir, root); err == nil || !strings.Contains(err.Error(), "fixture must be a regular file") {
		t.Fatalf("nonregular deadline fixture returned %v", err)
	}
}

func verifyRPCOwnedRequestOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	const name = "request_reuse_test.go"
	path := filepath.Join(dir, name)
	projected, err := os.ReadFile(replace[path])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(projected, []byte("package rpc\nimport (\"fmt\"; \"io\"; \"testing\")\nfunc BenchmarkLRPCOwnedRequest(")) ||
		bytes.Count(projected, []byte("\nfunc ")) != 1 || !bytes.HasSuffix(projected, []byte("}\n\n")) {
		t.Fatalf("unexpected owned-request projection %q", projected)
	}
	delete(replace, path)
	verifyOnlyListedTestFiles(t, replace, dir, name)
}

func TestRPCOwnedRequestOverlayRejectsChangedFixtureBoundaries(t *testing.T) {
	const pkg = "pkg/lux/rpc"
	const profile = "rpc-owned-request"
	const fixture = "request_reuse_test.go"
	const header = "package rpc\nimport (\"fmt\"; \"io\"; \"testing\")\n"
	const benchmark = "func BenchmarkLRPCOwnedRequest(b *testing.B) {}\n"
	if !strings.Contains(performanceProfile+"\n", "\nrpc-owned-request-overlay-v1 request_reuse_test.go\n") {
		t.Fatal("performance compile profile lacks the RPC owned-request fixture binding")
	}

	for _, tc := range []struct{ name, file, source, message string }{
		{"absent", "unrelated_test.go", "package rpc", "missing RPC owned-request test fixture"},
		{"missing declaration", fixture, header, "requires one BenchmarkLRPCOwnedRequest declaration"},
		{"duplicate declaration", fixture, header + benchmark + benchmark, "requires one BenchmarkLRPCOwnedRequest declaration"},
		{"renamed import", fixture, strings.Replace(header, `"fmt"`, `alias "fmt"`, 1) + benchmark, "requires the ordinary fmt import"},
		{"different package", fixture, strings.Replace(header, "package rpc", "package rpc_test", 1) + benchmark, "requires package rpc"},
		{"test setup", "unrelated_test.go", "package rpc\nfunc init() {}", "cannot isolate test package setup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := newBenchmarkRunTree(t)
			writeBenchmarkRunFixture(t, root, filepath.Join(pkg, tc.file), tc.source)
			_, cleanup, err := compileOverlay(root, pkg, false, profile)
			if cleanup != nil {
				t.Cleanup(func() {
					if err := cleanup(); err != nil {
						t.Error(err)
					}
				})
			}
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("owned-request fixture error = %v, want %s", err, tc.message)
			}
		})
	}
	root := newBenchmarkRunTree(t)
	if _, err := writeRPCOwnedRequestOverlay(filepath.Join(root, pkg), root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing RPC directory returned %v", err)
	}
	path := filepath.Join(root, pkg, fixture)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other_test.go", path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := compileOverlay(root, pkg, true, profile); err == nil || !strings.Contains(err.Error(), "fixture must be a regular file") {
		t.Fatalf("symlink fixture returned %v", err)
	}

}

func verifyAPICanonicalJSONOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	for _, fixture := range []struct{ file, retained string }{
		{"binary_bench_test.go", "func BenchmarkCanonicalJSONTyped("},
		{"request_scalar_bench_test.go", "func benchmarkStringArray("},
	} {
		path := filepath.Join(dir, fixture.file)
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := os.ReadFile(replace[path])
		if err != nil {
			t.Fatal(err)
		}
		offset := bytes.Index(original, []byte(fixture.retained))
		if offset < 0 || bytes.Index(projected, []byte(fixture.retained)) != offset || len(projected) != len(original) {
			t.Fatalf("canonical projection changed position in %s", fixture.file)
		}
	}
	for _, name := range []string{"local_input_bench_test.go", "identity_reader_bench_test.go"} {
		if replacement, present := replace[filepath.Join(dir, name)]; !present || replacement != "" {
			t.Fatalf("canonical binary retained unrelated file %s", name)
		}
	}
}

func TestAPICanonicalJSONOverlayRejectsChangedFixtureBoundaries(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.APIPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeAPICanonicalJSONOverlay(pkg, workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing API package error = %v", err)
	}
	if err := os.MkdirAll(pkg, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAPICanonicalJSONOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "missing API CanonicalJSON fixture") {
		t.Fatalf("absent CanonicalJSON fixtures returned %v", err)
	}
	for _, tc := range []struct{ file, source, expected string }{
		{"unrelated_test.go", "package api\nfunc init() {}", "cannot isolate test package setup"},
		{"unrelated_test.go", "package api\nfunc BenchmarkCanonicalJSONInteger() {}", "unexpected CanonicalJSON benchmark"},
		{"binary_bench_test.go", "package api\nfunc BenchmarkCanonicalJSONTyped() {}", "fixture declarations changed"},
		{"binary_bench_test.go", "package api\nfunc BenchmarkCanonicalJSONTyped() {}\nfunc BenchmarkCanonicalJSONTyped() {}", "duplicate CanonicalJSON declaration"},
	} {
		t.Run(tc.expected, func(t *testing.T) {
			writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.APIPackage, tc.file), tc.source)
			if _, err := writeAPICanonicalJSONOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), tc.expected) {
				t.Fatalf("fixture error = %v, want %s", err, tc.expected)
			}
			if err := os.Remove(filepath.Join(pkg, tc.file)); err != nil {
				t.Fatal(err)
			}
		})
	}
	file := filepath.Join(pkg, "unrelated_test.go")
	if err := os.Symlink("outside_test.go", file); err != nil {
		t.Fatal(err)
	}
	if _, err := writeAPICanonicalJSONOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "fixture must be a regular file") {
		t.Fatalf("symlink fixture returned %v", err)
	}
}

func TestAPICanonicalHelperProjectionKeepsTheHelperPosition(t *testing.T) {
	root := newBenchmarkRunTree(t)
	name := filepath.Join(benchlayout.APIPackage, "request_scalar_bench_test.go")
	source := "package api\nimport \"bytes\"\nfunc benchmarkStringArray() {}\n"
	writeBenchmarkRunFixture(t, root, name, source)
	output, err := projectAPICanonicalFixture(filepath.Join(root, name), filepath.Join(root, ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	projection, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	offset := strings.Index(source, "func benchmarkStringArray")
	if strings.Contains(string(projection), "\"bytes\"") || len(projection) != len(source) ||
		string(projection[offset:]) != source[offset:] {
		t.Fatalf("incorrect helper projection %q", projection)
	}
}

func verifyLuviaMergeDestinationOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	verifyOnlyListedTestFiles(t, replace, dir, "merger_bench_test.go", "primary_keys_bench_test.go", "primary_keys_baseline_test.go")
}

func verifyOnlyListedTestFiles(t *testing.T, replace map[string]string, dir string, fixtures ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	omitted := 0
	for _, entry := range entries {
		name := entry.Name()
		replacement, present := replace[filepath.Join(dir, name)]
		if slices.Contains(fixtures, name) {
			if present {
				t.Fatalf("overlay changed retained fixture %s", name)
			}
			continue
		}
		omit := strings.HasSuffix(name, "_test.go")
		if present != omit || replacement != "" {
			t.Fatalf("overlay entry for %s = %q, %v; omit=%v", name, replacement, present, omit)
		}
		if omit {
			omitted++
		}
	}
	if len(replace) != omitted {
		t.Fatalf("overlay contains an external path: %v", replace)
	}
}

func TestMergeDestinationOverlayRejectsMissingFixturesAndPackageSetup(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.LuviaPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeLuviaMergeDestinationOverlay(pkg, workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing Luvia package error = %v", err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuviaPackage, "merger_bench_test.go"), "package luvia")
	if _, err := writeLuviaMergeDestinationOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "missing Luvia merge destination test fixture") {
		t.Fatalf("missing required fixture error = %v", err)
	}
	unrelated := filepath.Join(benchlayout.LuviaPackage, "unrelated_test.go")
	writeBenchmarkRunFixture(t, root, unrelated, "package luvia\nfunc init() {}")
	if _, err := writeLuviaMergeDestinationOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "cannot isolate test package setup") {
		t.Fatalf("removing fixture with package setup returned %v", err)
	}
	if err := os.Remove(filepath.Join(root, unrelated)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("merger_bench_test.go", filepath.Join(root, unrelated)); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLuviaMergeDestinationOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "fixture must be a regular file") {
		t.Fatalf("removing symlink fixture returned %v", err)
	}
}

func verifyAPIOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	if len(replace) != 4 {
		t.Fatalf("API overlay changed %d files", len(replace))
	}
	for _, name := range []string{"identity_reader_bench_test.go", "identity_reader_fuzz_test.go", "identity_reader_test.go", "identity_value_test.go"} {
		if replacement, present := replace[filepath.Join(dir, name)]; !present || replacement != "" {
			t.Fatalf("API overlay did not omit %s", name)
		}
	}
}

func verifyLuviaOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	for _, name := range []string{"metrics_json_reference_test.go", "metrics_json_test.go", "primary_keys_baseline_test.go", "primary_keys_bench_test.go"} {
		if _, present := replace[filepath.Join(dir, name)]; present {
			t.Fatalf("Luvia overlay altered retained source %s", name)
		}
	}
	if v, present := replace[filepath.Join(dir, "convenience_keys_bench_test.go")]; !present || v != "" {
		t.Fatal("Luvia overlay retained an unrelated test file")
	}
	fixture := filepath.Join(dir, "metrics_retention_test.go")
	data, err := os.ReadFile(replace[fixture])
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	start := bytes.Index(data, []byte("func observationTestTrace("))
	if start < 0 || !bytes.Contains(original, data[start:]) || bytes.Count(data, []byte("\nfunc ")) != 1 {
		t.Fatalf("Luvia observation projection changed its retained declaration:\n%s", data)
	}
}

func TestAPIOverlayRejectsLostAndAdditionalBenchmarkFixtures(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.APIPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeAPIBenchmarkOverlay(pkg, workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing API fixture error = %v", err)
	}
	name := filepath.Join(benchlayout.APIPackage, "identity_reader_bench_test.go")
	for _, tc := range []struct{ content, want string }{
		{"package api\nfunc BenchmarkUnrelated() {}", "non-identity benchmark"},
		{"package api\nfunc TestMain() {}", "cannot isolate test package setup"},
	} {
		writeBenchmarkRunFixture(t, root, name, tc.content)
		if _, err := writeAPIBenchmarkOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("API fixture error = %v, want %s", err, tc.want)
		}
	}
}

func TestLuviaOverlayRejectsLostAndChangedBenchmarkFixtures(t *testing.T) {
	root := newBenchmarkRunTree(t)
	pkg := filepath.Join(root, benchlayout.LuviaPackage)
	workspace := filepath.Join(root, ".tmp")
	if _, err := writeLuviaBenchmarkOverlay(pkg, workspace); !os.IsNotExist(err) {
		t.Fatalf("missing Luvia package error = %v", err)
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.LuviaPackage, "regular.go"), "package luvia")
	if _, err := writeLuviaBenchmarkOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "missing Luvia primary and trace test fixture") {
		t.Fatalf("missing Luvia fixture error = %v", err)
	}
	name := filepath.Join(benchlayout.LuviaPackage, "metrics_retention_test.go")
	writeBenchmarkRunFixture(t, root, name, "package luvia\nfunc wrongName() {}")
	if _, err := writeLuviaBenchmarkOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "missing observationTestTrace") {
		t.Fatalf("missing Luvia observation helper error = %v", err)
	}
	target := filepath.Join(pkg, "helper_test.go")
	if err := os.Symlink("regular.go", target); err != nil {
		t.Fatal(err)
	}
	if _, err := writeLuviaBenchmarkOverlay(pkg, workspace); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlink substitution error = %v", err)
	}
}

func TestAPIRequestLogUnsupportedSourceKeepsExplicitStockBuild(t *testing.T) {
	root := newBenchmarkRunTree(t)
	for _, fixture := range apiIdentityFixtures {
		writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.APIPackage, fixture), "package api")
	}
	writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.APIPackage, "request_log_async_test.go"), "package api")
	_, _, err := compileOverlay(root, benchlayout.APIPackage, false, "api-request-log")
	if err == nil || !strings.Contains(err.Error(), "unsupported API request-log fixture") {
		t.Fatalf("unsupported API request-log fixture returned %v", err)
	}
	_, cleanup, err := compileOverlay(root, benchlayout.APIPackage, false, benchlayout.APINonIdentityProfile)
	if err != nil {
		t.Fatal(err)
	}
	if err := cleanup(); err != nil {
		t.Fatal(err)
	}
}

func verifyAPIRequestLogOverlay(t *testing.T, replace map[string]string, dir string) {
	t.Helper()
	source := filepath.Join(dir, apiRequestLogFixture)
	original, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	projected, changed := replace[source]
	if want := fmt.Sprintf("%x", sha256.Sum256(original)) == apiRequestLogSourceSHA; changed != want {
		t.Fatalf("request-log fixture replaced = %v, want %v", changed, want)
	}
	if changed {
		data, err := os.ReadFile(projected)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256(data)) != apiRequestLogProjectionSHA {
			t.Fatalf("request-log projection at %q: %v", projected, err)
		}
		delete(replace, source)
	}
	verifyAPIOverlay(t, replace, dir)
}

func TestAPIRequestLogProjection(t *testing.T) {
	base := []byte("package api\n\nimport (\n\t\"testing\"\n)\n")
	head := []byte(strings.Replace(string(base), "import (\n", "import (\n\t\"runtime\"\n", 1) +
		"\ntype interruptedRequestLogWriter struct {\n}\n")
	baseSHA := fmt.Sprintf("%x", sha256.Sum256(base))
	headSHA := fmt.Sprintf("%x", sha256.Sum256(head))
	before := bytes.Clone(head)
	if got, err := apiRequestLogProjection(head, baseSHA, headSHA); err != nil || !bytes.Equal(got, base) {
		t.Fatalf("request-log projection = %q, %v", got, err)
	}
	if !bytes.Equal(head, before) {
		t.Fatal("projection changed its input")
	}
	if _, err := apiRequestLogProjection(head, "", headSHA); err == nil || !strings.Contains(err.Error(), "does not match the verified projection") {
		t.Fatalf("request-log bad projection returned %v", err)
	}
	t.Run("overlay filesystem", func(t *testing.T) {
		root := newBenchmarkRunTree(t)
		pkg := filepath.Join(root, benchlayout.APIPackage)
		workspace := filepath.Join(root, "fixture-output")
		if _, err := writeAPIRequestLogOverlay(pkg, workspace, baseSHA, headSHA); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing identity fixture returned %v", err)
		}
		for _, fixture := range apiIdentityFixtures {
			writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.APIPackage, fixture), "package api")
		}
		if _, err := writeAPIRequestLogOverlay(pkg, workspace, baseSHA, headSHA); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing request-log fixture returned %v", err)
		}
		writeBenchmarkRunFixture(t, root, filepath.Join(benchlayout.APIPackage, apiRequestLogFixture), string(head))
		if _, err := writeAPIRequestLogOverlay(pkg, workspace, baseSHA, headSHA); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("missing projection directory returned %v", err)
		}
		if err := os.Mkdir(workspace, 0o700); err != nil {
			t.Fatal(err)
		}
		overlayPath, err := writeAPIRequestLogOverlay(pkg, workspace, baseSHA, headSHA)
		if err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(overlayPath)
		if err != nil {
			t.Fatal(err)
		}
		var overlay struct{ Replace map[string]string }
		if err := json.Unmarshal(content, &overlay); err != nil {
			t.Fatal(err)
		}
		projected, err := os.ReadFile(overlay.Replace[filepath.Join(pkg, apiRequestLogFixture)])
		if err != nil || !bytes.Equal(projected, base) {
			t.Fatalf("on-disk request-log projection = %q, %v", projected, err)
		}
	})
}
