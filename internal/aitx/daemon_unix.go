// internal/aitx/daemon_unix.go
//go:build !windows

package aitx

import "syscall"

func newDaemonAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
