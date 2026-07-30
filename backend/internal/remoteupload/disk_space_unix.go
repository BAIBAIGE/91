//go:build !windows

package remoteupload

import "golang.org/x/sys/unix"

func diskAvailableBytes(path string) (int64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(uint64(stat.Bavail) * uint64(stat.Bsize)), nil
}
