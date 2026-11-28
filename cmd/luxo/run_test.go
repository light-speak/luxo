package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunServerUsesGoCommand(t *testing.T) {
	if kind := os.Getenv("LUXO_TEST_RUN_ENTRY_CHILD"); kind != "" {
		err := runServer(nil, nil)
		if kind == "success" {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() == 0 {
				t.Fatalf("Go failure was not propagated: %v", err)
			}
			fmt.Println("launcher observed Go failure")
		}
		return
	}
	for _, kind := range []string{"success", "exit", "compile"} {
		t.Run(kind, func(t *testing.T) {
			prepareRunEntryProject(t, kind)
			before := addProjectSnapshot(t)
			command := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRunServerUsesGoCommand$", "-test.v")
			if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
				command.Args = append(command.Args, "-test.gocoverdir="+directory.Value.String())
			}
			command.Env = append(os.Environ(), "LUXO_TEST_RUN_ENTRY_CHILD="+kind)
			command.Stdin = strings.NewReader("input from caller\n")
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			if err := command.Run(); err != nil {
				t.Fatalf("launcher child failed: %v\n%s\n%s", err, stdout.String(), stderr.String())
			}
			assertRunEntryStreams(t, kind, stdout.String(), stderr.String())
			assertAddProjectSnapshot(t, before)
		})
	}
}

func prepareRunEntryProject(t *testing.T, kind string) {
	t.Helper()
	enterCacheGenerationScratch(t)
	// The controlled fixture owns its workspace and uses only the standard library.
	t.Setenv("GOWORK", "auto")
	if err := os.MkdirAll("lucis/app", 0700); err != nil {
		t.Fatal(err)
	}
	source := "package main\nimport (\"fmt\"; \"io\"; \"os\")\nfunc main() { value, err := io.ReadAll(os.Stdin); if err != nil { panic(err) }; fmt.Printf(\"run stdout: %s\", value); fmt.Fprintln(os.Stderr, \"run stderr\") }\n"
	if kind == "exit" {
		source = "package main\nimport (\"fmt\"; \"os\")\nfunc main() { fmt.Fprintln(os.Stderr, \"run exit sentinel\"); os.Exit(23) }\n"
	} else if kind == "compile" {
		source = "package main\nfunc main() { missingRunEntrySymbol() }\n"
	}
	for path, data := range map[string]string{
		"go.mod":                "module example.com/cli-run-probe\n\ngo 1.27.4\n",
		"go.work":               "go 1.27.4\n\nuse .\n",
		"lucis/app/main.gen.go": source,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func assertRunEntryStreams(t *testing.T, kind, stdout, stderr string) {
	t.Helper()
	if kind == "success" {
		if !strings.Contains(stdout, "run stdout: input from caller\n") || stderr != "run stderr\n" {
			t.Fatalf("Go standard streams were not inherited: %q %q", stdout, stderr)
		}
		return
	}
	if !strings.Contains(stdout, "launcher observed Go failure") {
		t.Fatalf("missing observed Go error: %s", stdout)
	}
	marker := "run exit sentinel"
	if kind == "compile" {
		marker = "undefined: missingRunEntrySymbol"
	}
	if !strings.Contains(stderr, marker) {
		t.Fatalf("Go diagnostic was not inherited: %q", stderr)
	}
}

func TestRunServerChecksEntryBeforeToolLookup(t *testing.T) {
	enterCacheGenerationScratch(t)
	t.Setenv("PATH", filepath.Join(mustWorkingDirectory(t), "no-go-here"))
	before := addProjectSnapshot(t)
	err := runServer(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "main.gen.go not found") {
		t.Fatalf("missing entry did not precede tool lookup: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}

func TestRunServerPropagatesToolLookupFailure(t *testing.T) {
	prepareRunEntryProject(t, "success")
	t.Setenv("PATH", filepath.Join(mustWorkingDirectory(t), "no-go-here"))
	before := addProjectSnapshot(t)
	if err := runServer(nil, nil); !errors.Is(err, exec.ErrNotFound) {
		t.Fatalf("Go tool lookup failure was lost: %v", err)
	}
	assertAddProjectSnapshot(t, before)
}
