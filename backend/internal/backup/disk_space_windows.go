//go:build windows

package backup

import (
	"syscall"
	"unsafe"
)

var (
	kernel32Backup            = syscall.NewLazyDLL("kernel32.dll")
	getDiskFreeSpaceExWBackup = kernel32Backup.NewProc("GetDiskFreeSpaceExW")
)

func availableDiskBytes(path string) (int64, error) {
	ptr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	result, _, callErr := getDiskFreeSpaceExWBackup.Call(
		uintptr(unsafe.Pointer(ptr)),
		uintptr(unsafe.Pointer(&available)),
		0,
		0,
	)
	if result == 0 {
		return 0, callErr
	}
	if available > uint64(^uint64(0)>>1) {
		return int64(^uint64(0) >> 1), nil
	}
	return int64(available), nil
}
