package storage

import (
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"testing"
	"testing/iotest"

	"github.com/google/uuid"
)

func TestGenerateKeyUUIDFailure(t *testing.T) {
	if os.Getenv("LUXO_STORAGE_UUID_FAILURE_TEST") != "1" {
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(executable, "-test.run=^TestGenerateKeyUUIDFailure$", "-test.v")
		command.Env = append(os.Environ(), "LUXO_STORAGE_UUID_FAILURE_TEST=1")
		if directory := flag.Lookup("test.gocoverdir"); directory != nil && directory.Value.String() != "" {
			command.Args = append(command.Args, "-test.gocoverdir="+directory.Value.String())
		}
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatal(string(output), err)
		}
		return
	}
	uuid.DisableRandPool()
	uuid.SetRand(iotest.ErrReader(io.ErrUnexpectedEOF))
	t.Cleanup(func() { uuid.SetRand(nil) })
	key, err := GenerateKey("user", "avatar", "photo.png")
	if key != "" || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal("entropy failure produced key or lost cause", key, err)
	}
}
