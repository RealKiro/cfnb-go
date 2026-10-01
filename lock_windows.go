//go:build windows

package main

import (
	"os"
	"syscall"
)

var (
	lockHandle   syscall.Handle
	lockFilePath string
)

// acquireSingleInstance Windows 下以独占（不共享）方式打开锁文件实现单实例
func acquireSingleInstance(path string) bool {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	// dwShareMode = 0：其他进程再次打开会失败，等价于互斥锁
	h, err := syscall.CreateFile(
		p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		0,
		nil,
		syscall.OPEN_ALWAYS,
		syscall.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return false
	}
	lockHandle = h
	lockFilePath = path
	return true
}

// releaseSingleInstance 关闭句柄并清理锁文件
func releaseSingleInstance() {
	if lockHandle == 0 {
		return
	}
	_ = syscall.CloseHandle(lockHandle)
	lockHandle = 0
	if lockFilePath != "" {
		_ = os.Remove(lockFilePath)
		lockFilePath = ""
	}
}
