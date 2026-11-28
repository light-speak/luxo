package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeployGeneratesProjectArtifacts(t *testing.T) {
	for _, target := range []string{"compose", "helm"} {
		for _, module := range []string{"example.com/deploy-probe", "deploy-probe", "", "\"example.com/deploy-probe\""} {
			t.Run(target+"/"+module, func(t *testing.T) {
				prepareDeployProject(t, module)
				output, err := captureCLIResponse(t, func() error { return runDeploy(nil, []string{target}) })
				if err != nil {
					t.Fatal(err)
				}
				name := "deploy-probe"
				if module == "" {
					name = "luxo"
				}
				if target == "compose" {
					assertDeployArtifactContains(t, "Dockerfile", "FROM golang:1.27.4-alpine", "./lucis/user/", "./lucis/post/", "./lucis/gateway/")
					assertDeployArtifactContains(t, "docker-compose.yml", "container_name: "+name+"-pg", "DATABASE_PREFIX: "+name, "target: user-run", "target: post-run", "target: gateway-run")
					if !strings.Contains(output, "Deploy files generated") {
						t.Fatalf("missing compose completion: %s", output)
					}
				} else {
					assertDeployArtifactContains(t, "deploy/helm/Chart.yaml", "apiVersion: v2", "name: "+name)
					for _, module := range []string{"user", "post"} {
						for _, kind := range []string{"deployment", "service"} {
							assertDeployArtifactContains(t, "deploy/helm/templates/module-"+module+"-"+kind+".yaml", "apiVersion:")
						}
					}
					if !strings.Contains(output, "Helm chart generated") {
						t.Fatalf("missing helm completion: %s", output)
					}
				}
			})
		}
	}
}

func TestDeployComposeUsesGenerationBackendSelection(t *testing.T) {
	for _, selection := range []string{"dotenv", "memory", "invalid"} {
		t.Run(selection, func(t *testing.T) {
			prepareDeployProject(t, "example.com/deploy-probe")
			writeCoverageFile(t, "origin/watch.luxo", "api watchValue(): Int @stream @native\n")
			writeCoverageFile(t, ".env", "LUXO_EVENT_BACKEND=nats\n")
			t.Setenv("LUXO_QUEUE_BACKEND", "none")
			t.Setenv("LUXO_CACHE_BACKEND", "none")
			t.Setenv("LUXO_EVENT_BACKEND", selection)
			if selection == "dotenv" {
				if err := os.Unsetenv("LUXO_EVENT_BACKEND"); err != nil {
					t.Fatal(err)
				}
			}
			output, err := captureCLIResponse(t, func() error { return runDeploy(nil, []string{"compose"}) })
			if selection == "invalid" {
				if err == nil || !strings.Contains(err.Error(), "unsupported events backend") || output != "" {
					t.Fatalf("invalid selection: error=%v output=%q", err, output)
				}
				for _, file := range []string{"Dockerfile", "docker-compose.yml"} {
					if _, err := os.Stat(file); !os.IsNotExist(err) {
						t.Fatalf("invalid selection wrote %s: %v", file, err)
					}
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile("docker-compose.yml")
			if err != nil {
				t.Fatal(err)
			}
			want := 0
			if selection == "dotenv" {
				want = 2 // The stream module and gateway both construct the event bus.
			}
			if got := strings.Count(string(data), "NATS_URL: nats://nats:4222"); got != want {
				t.Fatalf("NATS consumers = %d, want %d:\n%s", got, want, data)
			}
		})
	}
}

func TestDeployHelmUsesGenerationBackendSelection(t *testing.T) {
	prepareDeployProject(t, "example.com/deploy-probe")
	writeCoverageFile(t, "origin/watch.luxo", "api watchValue(): Int @stream @native\n")
	writeCoverageFile(t, "origin/read.luxo", `api readValue(): Int @cache(policy: "preview") { return 1 }`)
	writeCoverageFile(t, "luxo.cache.json", `{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"1m"}}}`)
	t.Setenv("LUXO_EVENT_BACKEND", "nats")
	t.Setenv("LUXO_QUEUE_BACKEND", "nats")
	t.Setenv("LUXO_CACHE_BACKEND", "redis")
	if _, err := captureCLIResponse(t, func() error { return runDeploy(nil, []string{"helm"}) }); err != nil {
		t.Fatal(err)
	}
	root := "deploy/helm/templates/"
	assertDeployArtifactContains(t, root+"gateway-deployment.yaml", "name: NATS_URL")
	assertDeployArtifactContains(t, root+"module-watch-deployment.yaml", "name: NATS_URL")
	assertDeployArtifactContains(t, root+"module-user-deployment.yaml", "name: NATS_URL")
	assertDeployArtifactContains(t, root+"module-read-deployment.yaml", "name: NATS_URL", "name: REDIS_URL",
		"name: LUXO_CACHE_PROJECT", "name: LUXO_CACHE_ENVIRONMENT", "name: LUXO_CACHE_REVISION",
		"name: LUXO_CACHE_ORIGIN_CONCURRENCY", "name: LUXO_CACHE_ORIGIN_MAX_WAITERS", "name: LUXO_CACHE_ORIGIN_WAIT_TIMEOUT")
}

func prepareDeployProject(t *testing.T, module string) {
	t.Helper()
	enterCacheGenerationScratch(t)
	if err := os.Mkdir("origin", 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"user", "post"} {
		source := "model " + strings.ToUpper(name[:1]) + name[1:] + " @noTime { id: Int @id\nname: String\n}\n"
		if err := os.WriteFile(filepath.Join("origin", name+".luxo"), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if module != "" {
		if err := os.WriteFile("go.mod", []byte("module "+module+"\n\ngo 1.27.4\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertDeployArtifactContains(t *testing.T, path string, expected ...string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range expected {
		if !strings.Contains(string(data), value) {
			t.Errorf("%s missing %q", path, value)
		}
	}
}

func TestDeployInputErrorsPreserveProject(t *testing.T) {
	for _, target := range []string{"compose", "helm", "unknown"} {
		for _, kind := range []string{"missing_origin", "syntax", "semantic"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				enterCacheGenerationScratch(t)
				if kind != "missing_origin" {
					if err := os.Mkdir("origin", 0700); err != nil {
						t.Fatal(err)
					}
					source := "model Broken {"
					if kind == "semantic" {
						source = "model Broken { value: MissingType }"
					}
					if err := os.WriteFile("origin/main.luxo", []byte(source), 0600); err != nil {
						t.Fatal(err)
					}
				}
				before := addProjectSnapshot(t)
				output, err := captureCLIResponse(t, func() error { return runDeploy(nil, []string{target}) })
				if err == nil || output != "" {
					t.Fatalf("invalid deployment accepted: %v %q", err, output)
				}
				assertAddProjectSnapshot(t, before)
			})
		}
	}
}

func TestDeployEmptyModulesDoNotWriteFiles(t *testing.T) {
	for _, target := range []string{"compose", "helm"} {
		t.Run(target, func(t *testing.T) {
			enterCacheGenerationScratch(t)
			if err := os.Mkdir("origin", 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("origin/common.luxo", []byte("type Payload { value: String }"), 0600); err != nil {
				t.Fatal(err)
			}
			before := addProjectSnapshot(t)
			output, err := captureCLIResponse(t, func() error { return runDeploy(nil, []string{target}) })
			if err != nil || !strings.Contains(output, "No modules found") {
				t.Fatalf("empty deployment result: %v %q", err, output)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func TestDeployWriteErrorsDoNotPrintCompletion(t *testing.T) {
	for _, target := range []string{"compose", "helm"} {
		t.Run(target, func(t *testing.T) {
			prepareDeployProject(t, "example.com/deploy-probe")
			if target == "compose" {
				for _, path := range []string{"Dockerfile", "docker-compose.yml"} {
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
			} else if err := os.WriteFile("deploy", []byte("keep existing deployment\n"), 0600); err != nil {
				t.Fatal(err)
			}
			before := addProjectSnapshot(t)
			output, err := captureCLIResponse(t, func() error { return runDeploy(nil, []string{target}) })
			if err == nil || strings.Contains(output, "generated") {
				t.Fatalf("write failure printed completion: %v %q", err, output)
			}
			assertAddProjectSnapshot(t, before)
		})
	}
}

func TestDeployInvalidModulePathPreservesProject(t *testing.T) {
	for _, target := range []string{"compose", "helm"} {
		for _, kind := range []string{"bad-quote", "extra-token", "read-error"} {
			t.Run(target+"/"+kind, func(t *testing.T) {
				prepareDeployProject(t, "example.com/deploy-probe")
				writeInvalidModulePathFixture(t, kind)
				for _, path := range []string{"Dockerfile", "docker-compose.yml", "deploy/helm/Chart.yaml"} {
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("keep existing "+path), 0600); err != nil {
						t.Fatal(err)
					}
				}
				before := addProjectSnapshot(t)
				output, err := captureCLIResponse(t, func() error { return runDeploy(nil, []string{target}) })
				if err == nil || !strings.Contains(err.Error(), "go.mod") || output != "" {
					t.Errorf("invalid go.mod reported deploy success: %v %q", err, output)
				}
				assertAddProjectSnapshot(t, before)
			})
		}
	}
}
