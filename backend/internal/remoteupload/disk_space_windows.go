//go:build windows

package remoteupload

import (
	"errors"
	"path/filepath"
	"syscall"
	"unsafe"
)

func diskAvailableBytes(path string) (int64, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return 0, err
	}
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	proc := kernel32.NewProc("GetDiskFreeSpaceExW")
	var available uint64
	result, _, _ := proc.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&available)),
		0,
		0,
	)
	if result == 0 {
		return 0, errors.New("GetDiskFreeSpaceExW failed")
	}
	return int64(available), nil
}
