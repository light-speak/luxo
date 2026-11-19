package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

const migrationGenerationLockPath = "migrations/.luxo-migrate.lock"

func withMigrationGenerationLock(ctx context.Context, generate func() error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(".")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	created := false
	if err := root.Mkdir("migrations", 0755); err == nil {
		created = true
	} else if !errors.Is(err, os.ErrExist) {
		return err
	}
	defer func() {
		if err != nil && created {
			err = errors.Join(err, root.Remove("migrations"))
		}
	}()
	file, err := lockMigrationGeneration(ctx, root, &created)
	if err != nil {
		return err
	}
	defer func() {
		// Unlink while locked; waiters must retry if their inode is obsolete.
		err = errors.Join(err, root.Remove(migrationGenerationLockPath), file.Close())
	}()
	if err := ctx.Err(); err != nil {
		return err
	}
	return generate()
}

func lockMigrationGeneration(ctx context.Context, root *os.Root, created *bool) (*os.File, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := openMigrationGenerationLock(root)
		if errors.Is(err, os.ErrNotExist) {
			if err := root.Mkdir("migrations", 0755); err == nil {
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
			current, checkErr := migrationGenerationLockCurrent(root, file)
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

func openMigrationGenerationLock(root *os.Root) (*os.File, error) {
	file, err := root.OpenFile(migrationGenerationLockPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if !errors.Is(err, os.ErrExist) {
		return file, err
	}
	info, err := root.Lstat(migrationGenerationLockPath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 {
		return nil, fmt.Errorf("migration generation lock must be an empty regular file: %s", migrationGenerationLockPath)
	}
	return root.OpenFile(migrationGenerationLockPath, os.O_RDWR, 0)
}

func migrationGenerationLockCurrent(root *os.Root, file *os.File) (bool, error) {
	info, err := file.Stat()
	if err != nil {
		return false, err
	}
	pathInfo, err := root.Lstat(migrationGenerationLockPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Size() != 0 || !pathInfo.Mode().IsRegular() {
		return false, fmt.Errorf("migration generation lock must be an empty regular file: %s", migrationGenerationLockPath)
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
