package main

import "syscall"

func fixtureProcessSession(pid int) (int, error) {
	session, _, err := syscall.Syscall(syscall.SYS_GETSID, uintptr(pid), 0, 0)
	if err != 0 {
		return 0, err
	}
	return int(session), nil
}
