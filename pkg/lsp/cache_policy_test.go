package lsp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const cachedEditorSource = `api cached(): Int @cache(policy: "preview") { return 1 }`

func TestEditorRejectsInlineCacheContract(t *testing.T) {
	root := cacheEditorProject(t)
	writeEditorPolicy(t, root, "1m")
	store := NewDocumentStore()
	doc := store.Open(PathToURI(filepath.Join(root, "api.luxo")), 1, `api cached(): Int @cache(ttl: 60) { return 1 }`)
	found := false
	for _, diagnostic := range doc.Diagnostics() {
		found = found || strings.Contains(diagnostic.Message, "@cache requires an explicit policy")
	}
	if !found || len(doc.Result.CachePolicies) != 0 {
		t.Fatal("editor accepted an implicit cache contract")
	}
	description := directiveDescription("cache")
	if !strings.Contains(description, `@cache(policy: "name")`) || strings.Contains(description, "@cache(ttl:") {
		t.Fatalf("stale cache guidance: %s", description)
	}
}

func cacheEditorProject(t *testing.T) string {
	t.Helper()
	base, err := filepath.Abs("../../.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(base, "lsp-cache-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if filepath.Dir(root) != base {
			t.Fatal("refusing cleanup outside project scratch directory")
		}
		if err := os.RemoveAll(root); err != nil {
			t.Error(err)
		}
	})
	return root
}

func writeEditorPolicy(t *testing.T, root, ttl string) {
	t.Helper()
	raw := `{"version":1,"policies":{"preview":{"mode":"ttl","ttl":"` + ttl + `"}}}`
	if err := os.WriteFile(filepath.Join(root, "luxo.cache.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertEditorPolicy(t *testing.T, doc *Document, ttl time.Duration) {
	t.Helper()
	for _, diagnostic := range doc.Diagnostics() {
		if diagnostic.Severity == 1 {
			t.Fatalf("unexpected diagnostic: %s", diagnostic.Message)
		}
	}
	if doc.Result == nil || len(doc.Result.CachePolicies) != 1 {
		t.Fatal("editor did not resolve the policy")
	}
	for _, policy := range doc.Result.CachePolicies {
		if policy.TTL != ttl {
			t.Fatalf("wrong project policy: %v, want %v", policy.TTL, ttl)
		}
	}
}

func TestEditorCachePoliciesAreProjectScoped(t *testing.T) {
	store := NewDocumentStore()
	first, second := cacheEditorProject(t), cacheEditorProject(t)
	writeEditorPolicy(t, first, "1s")
	writeEditorPolicy(t, second, "2s")
	firstURI := PathToURI(filepath.Join(first, "origin/main/api.luxo"))
	secondURI := PathToURI(filepath.Join(second, "origin/main/api.luxo"))
	store.Open(firstURI, 1, cachedEditorSource)
	store.Open(secondURI, 1, cachedEditorSource)
	assertEditorPolicy(t, store.Get(firstURI), time.Second)
	assertEditorPolicy(t, store.Get(secondURI), 2*time.Second)
	if store.Get(firstURI).Result == store.Get(secondURI).Result {
		t.Fatal("independent projects share a semantic compilation")
	}
	writeEditorPolicy(t, first, "invalid")
	store.analyzeAll()
	if len(store.Get(firstURI).Result.Errors) == 0 {
		t.Fatal("invalid policy did not fail the affected project")
	}
	assertEditorPolicy(t, store.Get(secondURI), 2*time.Second)
}

func TestEditorCachePolicyFailuresDoNotRetainOldCatalog(t *testing.T) {
	root := cacheEditorProject(t)
	writeEditorPolicy(t, root, "1s")
	store := NewDocumentStore()
	uri := PathToURI(filepath.Join(root, "origin/api.luxo"))
	assertEditorPolicy(t, store.Open(uri, 1, cachedEditorSource), time.Second)
	path := filepath.Join(root, "luxo.cache.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"policies":{},"backend":"redis"}`), 0600); err != nil {
		t.Fatal(err)
	}
	store.analyzeAll()
	if !strings.Contains(editorDiagnostics(store.Get(uri)), "unknown cache catalog field") {
		t.Fatal("invalid catalog was silently ignored")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	store.analyzeAll()
	if !strings.Contains(editorDiagnostics(store.Get(uri)), "unknown cache policy") {
		t.Fatal("deleted policy remained available")
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	store.analyzeAll()
	if !strings.Contains(editorDiagnostics(store.Get(uri)), "read cache policies") {
		t.Fatal("unreadable catalog was silently ignored")
	}
}

func editorDiagnostics(doc *Document) string {
	var messages []string
	for _, diagnostic := range doc.Diagnostics() {
		messages = append(messages, diagnostic.Message)
	}
	return strings.Join(messages, "\n")
}

func TestEditorCachePolicyWatchReloads(t *testing.T) {
	root := cacheEditorProject(t)
	server, output := newTestServer()
	uri := PathToURI(filepath.Join(root, "origin/main/api.luxo"))
	server.docs.Open(uri, 1, cachedEditorSource)
	done := make(chan struct{}, 1)
	analyzed := server.docs.OnAnalyzed
	server.docs.OnAnalyzed = func() { analyzed(); done <- struct{}{} }
	for _, ttl := range []string{"1s", "2s"} {
		writeEditorPolicy(t, root, ttl)
		output.Reset()
		notifyEditorPolicyChange(t, server, root, 2, done)
		want, _ := time.ParseDuration(ttl)
		assertEditorPolicy(t, server.docs.Get(uri), want)
		if !strings.Contains(output.String(), "publishDiagnostics") {
			t.Fatal("updated diagnostics did not reach the client")
		}
	}
	if err := os.Remove(filepath.Join(root, "luxo.cache.json")); err != nil {
		t.Fatal(err)
	}
	notifyEditorPolicyChange(t, server, root, 3, done)
	if !strings.Contains(editorDiagnostics(server.docs.Get(uri)), "unknown cache policy") {
		t.Fatal("catalog deletion did not invalidate editor diagnostics")
	}
}

func notifyEditorPolicyChange(t *testing.T, server *Server, root string, event int, done <-chan struct{}) {
	t.Helper()
	params, err := json.Marshal(DidChangeWatchedFilesParams{Changes: []FileEvent{{URI: PathToURI(filepath.Join(root, "luxo.cache.json")), Type: event}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.handleDidChangeWatchedFiles(&Request{Params: params}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("policy watch did not trigger analysis")
	}
}

func TestEditorRegistersCachePolicyWatcher(t *testing.T) {
	server, output := newTestServer()
	if err := server.handleInitialized(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "**/luxo.cache.json") {
		t.Fatal("cache catalog changes are not watched")
	}
}

func TestEditorProjectRoot(t *testing.T) {
	for _, name := range []string{"/project/origin/main/api.luxo", "/project/origin/api.luxo", "/project/api.luxo"} {
		if got := documentProjectRoot(filepath.FromSlash(name)); got != filepath.FromSlash("/project") {
			t.Fatalf("root for %s: %s", name, got)
		}
	}
	if got := documentProjectRoot("api.luxo"); got != "." {
		t.Fatalf("relative root: %s", got)
	}
}

func TestEditorCachePoliciesIncludeUnopenedSiblings(t *testing.T) {
	root := cacheEditorProject(t)
	writeEditorPolicy(t, root, "3s")
	dir := filepath.Join(root, "origin/main")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "helper.luxo"), []byte(`fn answer(): Int { return 42 }`), 0600); err != nil {
		t.Fatal(err)
	}
	store := NewDocumentStore()
	doc := store.Open(PathToURI(filepath.Join(dir, "api.luxo")), 1, `api cached(): Int @cache(policy: "preview") { return answer() }`)
	assertEditorPolicy(t, doc, 3*time.Second)
	if len(doc.Result.Files) != 2 {
		t.Fatal("policy analysis lost sibling declarations")
	}
}

func TestEditorSiblingDiscoveryIgnoresOpenAndUnreadableFiles(t *testing.T) {
	root := cacheEditorProject(t)
	dir := filepath.Join(root, "origin", "main")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"api.luxo":    "disk contents must not replace the open buffer",
		"notes.txt":   "not a source file",
		"helper.luxo": "fn answer(): Int { return 42 }",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(dir, "missing-target"), filepath.Join(dir, "broken.luxo")); err != nil {
		t.Fatal(err)
	}
	open := &Document{URI: PathToURI(filepath.Join(dir, "api.luxo")), Content: "unsaved buffer"}
	store := NewDocumentStore()
	discovered := store.discoverSiblingFiles([]*Document{open})
	if len(discovered) != 1 || discovered[0].URI != PathToURI(filepath.Join(dir, "helper.luxo")) || discovered[0].Content != "fn answer(): Int { return 42 }" {
		t.Fatalf("unexpected discovered files: %+v", discovered)
	}
	if open.Content != "unsaved buffer" {
		t.Fatal("discovery changed the open document")
	}
}
