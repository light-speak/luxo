package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/light-speak/luxo/pkg/codegen"
)

const helmChartDirectory = "deploy/helm"

type helmChartChange struct {
	path     string
	previous fs.FileInfo
	staged   string
	backup   string
}

func publishHelmChart(ctx context.Context, files map[string][]byte) (paths, retired []string, err error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	created, err := prepareHelmDirectories(root)
	defer func() {
		if err != nil {
			for _, path := range slices.Backward(created) {
				err = errors.Join(err, root.Remove(path))
			}
		}
	}()
	if err != nil {
		return nil, nil, err
	}
	started := false
	err = withGenerationLock(ctx, helmChartDirectory+"/.luxo-helm.lock.tmp", func(root *os.Root) error {
		chart, err := root.OpenRoot(helmChartDirectory)
		if err != nil {
			return err
		}
		started = true
		paths, retired, err = applyHelmChart(chart, files)
		return errors.Join(err, chart.Close())
	})
	if err != nil && !started {
		return nil, nil, fmt.Errorf("write %s: %w", helmChartDirectory, err)
	}
	return paths, retired, err
}

func prepareHelmDirectories(root *os.Root) (created []string, err error) {
	for _, path := range []string{"deploy", helmChartDirectory, helmChartDirectory + "/templates"} {
		info, err := root.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			if mkdirErr := root.Mkdir(path, 0755); mkdirErr == nil {
				created = append(created, path)
				continue
			} else if !errors.Is(mkdirErr, fs.ErrExist) {
				return created, fmt.Errorf("mkdir %s: %w", path, mkdirErr)
			}
			info, err = root.Lstat(path)
		}
		if err != nil {
			return created, err
		}
		if !info.IsDir() {
			return created, fmt.Errorf("Helm output path must be a real directory: %s", path)
		}
	}
	return created, nil
}

func applyHelmChart(root *os.Root, files map[string][]byte) (paths, retired []string, err error) {
	stage := ".luxo-helm-" + rand.Text() + ".tmp"
	if err := root.Mkdir(stage, 0700); err != nil {
		return nil, nil, fmt.Errorf("write %s: %w", helmChartDirectory, err)
	}
	recovered := true
	defer func() {
		if recovered {
			err = errors.Join(err, root.RemoveAll(stage))
		}
	}()
	names := slices.Sorted(maps.Keys(files))
	updates, removed, err := prepareHelmChanges(root, stage, names, files)
	if err != nil {
		return nil, nil, err
	}
	for i, change := range updates {
		if err := applyHelmChange(root, change); err != nil {
			rollbackErr := rollbackHelmChanges(root, updates[:i+1])
			recovered = rollbackErr == nil
			if !recovered {
				rollbackErr = fmt.Errorf("restore Helm chart from %s: %w", stage, rollbackErr)
			}
			return nil, nil, errors.Join(err, rollbackErr)
		}
	}
	paths = make([]string, 0, len(files))
	for _, name := range names {
		paths = append(paths, filepath.FromSlash(name))
	}
	return paths, removed, nil
}

func prepareHelmChanges(root *os.Root, stage string, names []string, files map[string][]byte) ([]helmChartChange, []string, error) {
	updates := make([]helmChartChange, 0, len(files))
	keep := make(map[string]fs.FileInfo, len(files))
	for i, name := range names {
		path := filepath.FromSlash(name)
		data := files[name]
		info, unchanged, err := helmTargetFile(root, path, data)
		if err != nil {
			return nil, nil, err
		}
		keep[strings.ToLower(path)] = info
		if unchanged {
			continue
		}
		mode := fs.FileMode(0644)
		if info != nil {
			mode = info.Mode().Perm()
		}
		staged := filepath.Join(stage, fmt.Sprintf("new-%d", i))
		file, err := root.OpenFile(staged, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
		if err != nil {
			return nil, nil, fmt.Errorf("write %s: %w", path, err)
		}
		if err := writeProjectFileContent(file, data); err != nil {
			return nil, nil, fmt.Errorf("write %s: %w", path, err)
		}
		if info != nil {
			if err := root.Chmod(staged, mode); err != nil {
				return nil, nil, fmt.Errorf("write %s: %w", path, err)
			}
		}
		updates = append(updates, helmChartChange{
			path: path, previous: info, staged: staged, backup: filepath.Join(stage, fmt.Sprintf("old-%d", i)),
		})
	}
	retired, err := retiredHelmModules(root, stage, files, keep)
	if err != nil {
		return nil, nil, err
	}
	removed := make([]string, 0, len(retired))
	for _, change := range retired {
		removed = append(removed, change.path)
	}
	return append(updates, retired...), removed, nil
}

func helmTargetFile(root *os.Root, path string, data []byte) (fs.FileInfo, bool, error) {
	info, err := root.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf("write %s: Helm output must be a regular file", path)
	}
	if info.Size() == int64(len(data)) {
		if previous, err := root.ReadFile(path); err == nil && bytes.Equal(previous, data) {
			return info, true, nil
		}
	}
	file, err := root.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		return nil, false, fmt.Errorf("write %s: %w", path, err)
	}
	return info, false, file.Close()
}

func retiredHelmModules(root *os.Root, stage string, files map[string][]byte, keep map[string]fs.FileInfo) ([]helmChartChange, error) {
	const templates = "templates"
	entries, err := fs.ReadDir(root.FS(), templates)
	if err != nil {
		return nil, err
	}
	var retired []helmChartChange
	for _, entry := range entries {
		name := entry.Name()
		if _, current := files["templates/"+name]; current || !entry.Type().IsRegular() || !isHelmModuleTemplate(name) {
			continue
		}
		path := filepath.Join(templates, name)
		info, err := entry.Info()
		if err != nil {
			return nil, err
		}
		// A case-only rename may refer to the current output on this filesystem.
		if current := keep[strings.ToLower(path)]; current != nil && os.SameFile(current, info) {
			continue
		}
		generated, err := isOwnedHelmTemplate(root, path)
		if err != nil {
			return nil, err
		}
		if generated {
			retired = append(retired, helmChartChange{
				path: path, previous: info, backup: filepath.Join(stage, fmt.Sprintf("retired-%d", len(retired))),
			})
		}
	}
	return retired, nil
}

func isHelmModuleTemplate(name string) bool {
	module, ok := strings.CutPrefix(name, "module-")
	if !ok {
		return false
	}
	for _, suffix := range []string{"-deployment.yaml", "-service.yaml"} {
		if name, ok := strings.CutSuffix(module, suffix); ok {
			return name != ""
		}
	}
	return false
}

func isOwnedHelmTemplate(root *os.Root, path string) (bool, error) {
	file, err := root.Open(path)
	if err != nil {
		return false, err
	}
	var header [len(codegen.HelmGeneratedFileComment) + 2]byte
	n, readErr := io.ReadFull(file, header[:])
	if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
		readErr = nil
	}
	line, _, ok := bytes.Cut(header[:n], []byte{'\n'})
	return ok && string(bytes.TrimSuffix(line, []byte{'\r'})) == codegen.HelmGeneratedFileComment,
		errors.Join(readErr, file.Close())
}

func applyHelmChange(root *os.Root, change helmChartChange) error {
	current, err := root.Lstat(change.path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if (change.previous == nil && current != nil) ||
		(change.previous != nil && (current == nil || !os.SameFile(change.previous, current) ||
			current.Size() != change.previous.Size() || !current.ModTime().Equal(change.previous.ModTime()))) {
		return fmt.Errorf("Helm output changed during generation: %s", change.path)
	}
	if change.previous != nil {
		if err := root.Rename(change.path, change.backup); err != nil {
			return fmt.Errorf("write %s: %w", change.path, err)
		}
	}
	if change.staged != "" {
		if err := root.Rename(change.staged, change.path); err != nil {
			return fmt.Errorf("write %s: %w", change.path, err)
		}
	}
	return nil
}

func rollbackHelmChanges(root *os.Root, changes []helmChartChange) error {
	var result error
	for _, change := range slices.Backward(changes) {
		if change.previous != nil {
			if _, err := root.Lstat(change.backup); !errors.Is(err, fs.ErrNotExist) {
				result = errors.Join(result, root.Rename(change.backup, change.path))
			}
		} else if _, err := root.Lstat(change.staged); errors.Is(err, fs.ErrNotExist) {
			result = errors.Join(result, root.Remove(change.path))
		} else {
			result = errors.Join(result, err)
		}
	}
	return result
}
