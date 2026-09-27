//go:build windows

package runtimecontrol

import (
	"golang.org/x/sys/windows"
	"unsafe"
)

var memoryStatus = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

func platformAvailableMemory() uint64 {
	var status struct {
		Length, Load                                               uint32
		TotalPhys, AvailablePhys, TotalPageFile, AvailablePageFile uint64
		TotalVirtual, AvailableVirtual, AvailableExtendedVirtual   uint64
	}
	status.Length = uint32(unsafe.Sizeof(status))
	result, _, _ := memoryStatus.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0
	}
	return min(status.AvailablePhys, status.AvailableVirtual)
}
