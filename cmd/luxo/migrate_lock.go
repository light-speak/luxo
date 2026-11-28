package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const migrationGenerationLockPath = "migrations/.luxo-migrate.lock"

func withMigrationGenerationLock(ctx context.Context, generate func() error) error {
	return withGenerationLock(ctx, migrationGenerationLockPath, func(_ *os.Root) error { return generate() })
}

func withGenerationLock(ctx context.Context, path string, generate func(*os.Root) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	directory := filepath.Dir(path)
	created := false
	if err := root.Mkdir(directory, 0755); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	defer func() {
		if err != nil && created {
			err = errors.Join(err, root.Remove(directory))
		}
	}()
	file, err := lockGenerationPath(ctx, root, path, &created)
	if err != nil {
		return err
	}
	defer func() {
		// Unlink while locked; waiters must retry if their inode is obsolete.
		err = errors.Join(err, root.Remove(path), file.Close())
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return generate(root)
}

func lockMigrationGeneration(ctx context.Context, root *os.Root, created *bool) (*os.File, error) {
	return lockGenerationPath(ctx, root, migrationGenerationLockPath, created)
}

func lockGenerationPath(ctx context.Context, root *os.Root, path string, created *bool) (*os.File, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := openGenerationLock(root, path)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir(filepath.Dir(path), 0755); err == nil {
				*created = true
			} else if !errors.Is(err, os.ErrExist) {
				return nil, err
			}
			continue
		}
		if err != nil {
			return nil, err
		}
		if err = waitMigrationGenerationLock(ctx, file); err == nil {
			current, checkErr := generationLockCurrent(root, path, file)
			err = checkErr
			if err == nil && current {
				return file, nil
			}
		}
		if err = errors.Join(err, file.Close()); err != nil {
			return nil, err
		}
	}
}

func openGenerationLock(root *os.Root, path string) (*os.File, error) {
	file, err := root.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if !errors.Is(err, os.ErrExist) {
		return file, err
	}
	info, err := root.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return nil, fmt.Errorf("generation lock must be an empty regular file: %s", path)
	}
	return root.OpenFile(path, os.O_RDWR, 0)
}

func migrationGenerationLockCurrent(root *os.Root, file *os.File) (bool, error) {
	return generationLockCurrent(root, migrationGenerationLockPath, file)
}

func generationLockCurrent(root *os.Root, path string, file *os.File) (bool, error) {
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	pathInfo, err := root.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 || !pathInfo.Mode().IsRegular() {
		return false, fmt.Errorf("generation lock must be an empty regular file: %s", path)
	}
	return os.SameFile(info, pathInfo), nil
}

func waitMigrationGenerationLock(ctx context.Context, file *os.File) error {
	if locked, err := tryMigrationGenerationLock(file); err != nil || locked {
		return err
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if locked, err := tryMigrationGenerationLock(file); err != nil || locked {
				return err
			}
		}
	}
}
