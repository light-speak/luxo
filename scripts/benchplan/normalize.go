package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/light-speak/luxo/internal/benchlayout"
)

func normalizeFiles(args []string) error {
	if len(args) != 3 || args[1] == args[2] {
		return errors.New("usage: benchplan --normalize <raw.txt> <normalized.txt> (different files)")
	}
	input, err := os.Open(args[1])
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(args[2], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	writeErr := normalize(input, output)
	return errors.Join(writeErr, output.Close())
}

func normalize(input io.Reader, output io.Writer) error {
	scanner := bufio.NewScanner(input)
	pending := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		completed, err := normalizeLine(line, &pending)
		if err != nil {
			return err
		}
		if completed != "" {
			if _, err := fmt.Fprintln(output, completed); err != nil {
				return err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if pending != "" {
		return fmt.Errorf("missing measurement for %s", pending)
	}
	return nil
}

func normalizeLine(line string, pending *string) (string, error) {
	const prefix = "pkg: github.com/light-speak/luxo/"
	if strings.HasPrefix(line, prefix) {
		line = prefix + benchlayout.Canonical(strings.TrimPrefix(line, prefix))
	}
	if line == "FAIL" || strings.HasPrefix(line, "--- FAIL:") {
		return "", errors.New("benchmark process failed")
	}
	if *pending != "" && (line == "PASS" || strings.HasPrefix(line, "pkg: ") || strings.HasPrefix(line, "Benchmark")) {
		return "", fmt.Errorf("missing measurement for %s before %q", *pending, line)
	}
	fields := strings.Fields(line)
	if strings.HasPrefix(line, "Benchmark") {
		if err := addMeasurement(make(inventory), []string{fields[0], "1", "0", "ns/op"}); err != nil {
			return "", err
		}
		if measurementTail(fields[1:]) {
			return line, nil
		}
		*pending = fields[0]
		return "", nil
	}
	if *pending != "" {
		if measurementTail(fields) {
			completed := *pending + "\t" + line
			*pending = ""
			return completed, nil
		}
		return "", nil
	}
	return line, nil
}
