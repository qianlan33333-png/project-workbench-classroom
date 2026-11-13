package main

import (
	"os"
	"path/filepath"
	"syscall"
)

func lockState(path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

func linkModules(home, modules string) error {
	path := filepath.Join(home, "node_modules")
	if target, err := os.Readlink(path); err == nil {
		if target == modules {
			return nil
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return os.Symlink(modules, path)
}
