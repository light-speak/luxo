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
					assertDeployArtifactContains(t, "Dockerfile", "FROM golang:1.27.1-alpine", "./lucis/user/", "./lucis/post/", "./lucis/gateway/")
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
		if err := os.WriteFile("go.mod", []byte("module "+module+"\n\ngo 1.27.1\n"), 0600); err != nil {
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
