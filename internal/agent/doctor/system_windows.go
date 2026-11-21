// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// OSBuild is Windows's version and build, such as "Windows 10, build
// 10.0.19045.5131 (22H2)", from RtlGetVersion and two named values of the
// registry's CurrentVersion key. It reads no other value: the same key
// holds the names of the owner and their organization.
func OSBuild() string {
	v := windows.RtlGetVersion()
	build := fmt.Sprintf("%d.%d.%d", v.MajorVersion, v.MinorVersion, v.BuildNumber)
	release := ""
	// WOW64_64KEY reads the 64-bit view from windows/386 too.
	if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Windows NT\CurrentVersion`, registry.QUERY_VALUE|registry.WOW64_64KEY); err == nil {
		if ubr, _, err := k.GetIntegerValue("UBR"); err == nil {
			build += fmt.Sprintf(".%d", ubr)
		}
		if dv, _, err := k.GetStringValue("DisplayVersion"); err == nil && dv != "" {
			release = " (" + dv + ")"
		}
		k.Close()
	}
	name := "Windows"
	switch {
	case v.MajorVersion == 10 && v.BuildNumber >= 22000:
		name = "Windows 11"
	case v.MajorVersion == 10:
		name = "Windows 10"
	}
	return name + ", build " + build + release
}

// The parts of winioctl.h DiskKind uses, which golang.org/x/sys/windows
// doesn't define. Neither struct holds a pointer, so one layout serves
// windows/386 too.
const (
	ioctlStorageQueryProperty        = 0x2d1400
	storageDeviceSeekPenaltyProperty = 7
	propertyStandardQuery            = 0
)

type storagePropertyQuery struct {
	propertyID uint32
	queryType  uint32
	additional [1]byte
}

type deviceSeekPenaltyDescriptor struct {
	version           uint32
	size              uint32
	incursSeekPenalty byte
}

// DiskKind says whether the disk that holds dir spins, from the seek penalty
// Windows reports for it: "hdd" when seeking costs time, as on a spinning
// disk, "ssd" when it doesn't, and "unknown" when the volume doesn't say,
// as a network share or a volume over several disks may not. It asks for
// the seek penalty alone, never the device's description, which holds its
// serial number.
func DiskKind(dir string) string {
	vol := filepath.VolumeName(dir)
	if len(vol) != 2 || vol[1] != ':' {
		return "unknown"
	}
	p, err := windows.UTF16PtrFromString(`\\.\` + vol)
	if err != nil {
		return "unknown"
	}
	// No access rights are needed to ask for a storage property.
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		return "unknown"
	}
	defer windows.CloseHandle(h)
	q := storagePropertyQuery{propertyID: storageDeviceSeekPenaltyProperty, queryType: propertyStandardQuery}
	var d deviceSeekPenaltyDescriptor
	var n uint32
	err = windows.DeviceIoControl(h, ioctlStorageQueryProperty, (*byte)(unsafe.Pointer(&q)), uint32(unsafe.Sizeof(q)),
		(*byte)(unsafe.Pointer(&d)), uint32(unsafe.Sizeof(d)), &n, nil)
	if err != nil || n < 9 {
		return "unknown"
	}
	if d.incursSeekPenalty != 0 {
		return "hdd"
	}
	return "ssd"
}
