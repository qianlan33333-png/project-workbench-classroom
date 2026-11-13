package main

import (
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func lockState(path string) (func(), error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	f, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, err
	}
	return func() { _ = windows.CloseHandle(f) }, nil
}

func linkModules(home, modules string) error {
	path := filepath.Join(home, "node_modules")
	if current, err := filepath.EvalSymlinks(path); err == nil {
		if current == modules {
			return nil
		}
		if err = os.Remove(path); err != nil {
			return err
		}
	}
	return exec.Command("cmd", "/c", "mklink", "/J", path, modules).Run()
}
