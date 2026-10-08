package scripts_test

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func candidateVersion(t *testing.T) string {
	t.Helper()
	version := strings.TrimSpace(readQualityFile(t, "../VERSION"))
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+-beta\.[1-9][0-9]*$`).MatchString(version) {
		t.Fatalf("invalid beta candidate version %q", version)
	}
	return version
}

func TestSDKCandidateVersions(t *testing.T) {
	version := candidateVersion(t)
	for _, name := range []string{"client", "react", "vite-plugin"} {
		t.Run(name, func(t *testing.T) {
			var manifest struct {
				Version          string
				PeerDependencies map[string]string
			}
			if err := json.Unmarshal([]byte(readQualityFile(t, "../packages/"+name+"/package.json")), &manifest); err != nil {
				t.Fatal(err)
			}
			if manifest.Version != version {
				t.Errorf("%s version = %s, want %s", name, manifest.Version, version)
			}
			if name != "client" && manifest.PeerDependencies["@luxojs/client"] != version {
				t.Error("companion packages must require the exact candidate client")
			}
		})
	}
	var dart struct{ Version string }
	if err := yaml.Unmarshal([]byte(readQualityFile(t, "../packages/dart/pubspec.yaml")), &dart); err != nil {
		t.Fatal(err)
	}
	if dart.Version != version {
		t.Errorf("Dart version = %s, want %s", dart.Version, version)
	}
}

func TestGoAdapterCandidateBindings(t *testing.T) {
	version := "v" + candidateVersion(t)
	internalRequirements := regexp.MustCompile(`(?m)^\s*(?:require\s+)?(github\.com/light-speak/luxo(?:/[^\s]+)?)\s+(v[^\s]+)`)
	for _, path := range []string{"cmd/luxo", "pkg/lux/pg", "pkg/lux/nats", "pkg/lux/cache/redis", "pkg/lux/storage/s3"} {
		t.Run(path, func(t *testing.T) {
			manifest := readQualityFile(t, "../"+path+"/go.mod")
			if !strings.HasPrefix(manifest, "module github.com/light-speak/luxo/"+path+"\n") {
				t.Fatal("module identity does not match its release tag prefix")
			}
			if !strings.Contains(manifest, "github.com/light-speak/luxo "+version+"\n") {
				t.Fatal("adapter must require the exact core candidate")
			}
			if strings.Contains(manifest, "replace ") {
				t.Fatal("published module must not depend on workspace replacements")
			}
			for _, dependency := range internalRequirements.FindAllStringSubmatch(manifest, -1) {
				if dependency[2] != version {
					t.Errorf("%s requires %s at %s, want %s", path, dependency[1], dependency[2], version)
				}
			}
		})
	}
}

func TestGoModuleReleaseTagsDocumented(t *testing.T) {
	version := "v" + candidateVersion(t)
	for _, document := range []string{"../README.md", "../README_CN.md"} {
		source := readQualityFile(t, document)
		for _, module := range []string{"cmd/luxo", "pkg/lux/pg", "pkg/lux/nats", "pkg/lux/cache/redis", "pkg/lux/storage/s3"} {
			if !strings.Contains(source, "`"+module+"/"+version+"`") {
				t.Errorf("%s must document the independent module tag %s/%s", document, module, version)
			}
		}
	}
}

func TestKotlinCandidateVersionBindings(t *testing.T) {
	version := candidateVersion(t)
	build := readQualityFile(t, "../packages/kotlin/build.gradle.kts")
	if !strings.Contains(build, `version = file("../../VERSION").readText().trim()`) {
		t.Fatal("Kotlin publishing must use the shared candidate version")
	}
	plugin := readQualityFile(t, "../packages/kotlin/gradle-plugin/build.gradle.kts")
	for _, required := range []string{"version = rootProject.version", `"luxoVersion" to version.toString()`} {
		if !strings.Contains(plugin, required) {
			t.Errorf("Gradle plugin lost candidate binding %q", required)
		}
	}
	for _, path := range []string{"../tests/kotlin-consumer/build.gradle.kts", "../tests/sdk-interop/kotlin/build.gradle.kts"} {
		if !strings.Contains(readQualityFile(t, path), `implementation("com.luxo:luxo-client:`+version+`")`) {
			t.Errorf("consumer %s must declare the candidate coordinate", path)
		}
	}
}

func TestCoreAndSwiftCandidateBindings(t *testing.T) {
	version := candidateVersion(t)
	if !strings.Contains(readQualityFile(t, "../cmd/luxo/root.go"), `var Version = "`+version+`"`) {
		t.Error("CLI default must identify the current candidate, not an unrelated dev build")
	}
	checks := 0
	for _, step := range readQualityWorkflow(t).Jobs["sdk-swift-conformance"].Steps {
		if step.Name == "Verify Swift candidate version" {
			checks++
			if step.Run != "cmp VERSION .tmp/luxo-swift/VERSION" || step.ContinueOnError {
				t.Error("Swift candidate mismatch must fail conformance")
			}
		}
	}
	if checks != 1 {
		t.Fatalf("expected one Swift candidate gate, got %d", checks)
	}
}

func TestSwiftCandidateCheckoutIsPinned(t *testing.T) {
	checkouts := 0
	for _, step := range readQualityWorkflow(t).Jobs["sdk-swift-conformance"].Steps {
		if step.With["repository"] != "light-speak/luxo-swift" {
			continue
		}
		checkouts++
		if step.With["ref"] != "d04f0e93efda40bd61825bba65954d1a06443a8a" {
			t.Errorf("Swift conformance must use the verified beta.1 candidate, got %q", step.With["ref"])
		}
		if step.With["path"] != ".tmp/luxo-swift" || step.With["persist-credentials"] != "false" || step.ContinueOnError {
			t.Error("Swift checkout must fail closed in the isolated candidate path without persisted credentials")
		}
	}
	if checkouts != 1 {
		t.Fatalf("expected one pinned Swift checkout, got %d", checkouts)
	}
}

func TestSDKInstallationExamplesMatchCandidate(t *testing.T) {
	version := candidateVersion(t)
	for path, coordinate := range map[string]string{
		"../packages/dart/README.md":   "luxo_client: " + version,
		"../packages/kotlin/README.md": `implementation("com.luxo:luxo-client:` + version + `")`,
	} {
		if !strings.Contains(readQualityFile(t, path), coordinate) {
			t.Errorf("%s must document the exact candidate coordinate %q", path, coordinate)
		}
	}
}
