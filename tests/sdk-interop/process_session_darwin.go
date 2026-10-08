package main

import "syscall"

func fixtureProcessSession(pid int) (int, error) {
	return syscall.Getsid(pid)
}
