//go:build windows

package process

import "golang.org/x/sys/windows"

func isPIDAlivePlatform(pid int) bool {
	const processQueryLimitedInformation = 0x1000
	handle, err := windows.OpenProcess(processQueryLimitedInformation, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(handle)
	return true
}
