//go:build !windows

package main

import (
	"os"
	"syscall"
)

var (
	lockFileHandle *os.File
	lockFilePath   string
)

// acquireSingleInstance 使用 flock 非阻塞独占锁实现单实例（Linux/macOS）
func acquireSingleInstance(path string) bool {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return false
	}
	lockFileHandle = f
	lockFilePath = path
	return true
}

// releaseSingleInstance 释放锁并清理锁文件
func releaseSingleInstance() {
	if lockFileHandle == nil {
		return
	}
	_ = syscall.Flock(int(lockFileHandle.Fd()), syscall.LOCK_UN)
	_ = lockFileHandle.Close()
	lockFileHandle = nil
	if lockFilePath != "" {
		_ = os.Remove(lockFilePath)
		lockFilePath = ""
	}
}
