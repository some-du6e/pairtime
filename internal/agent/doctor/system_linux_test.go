// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import "testing"

func TestMountSource(t *testing.T) {
	const mountinfo = `22 1 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw
35 22 0:42 /@home /home rw,relatime shared:2 - btrfs /dev/nvme0n1p3 rw,ssd,space_cache=v2,subvolid=257
36 22 0:43 / /tmp rw,nosuid,nodev shared:3 - tmpfs tmpfs rw,size=8G
37 22 0:44 / /var/lib/docker/overlay2/x/merged rw,relatime - overlay overlay rw,lowerdir=/l
38 22 0:45 / /mnt/usb rw,relatime shared:4 - btrfs /dev/disk\040one rw
not a mountinfo line
`
	tests := []struct {
		name         string
		major, minor uint32
		want         string
	}{
		{"a btrfs mount's own device number leads to the disk partition its source names", 0, 42, "/dev/nvme0n1p3"},
		{"tmpfs has no disk", 0, 43, ""},
		{"overlayfs has no disk of its own", 0, 44, ""},
		{"a source with a space, which mountinfo writes as an octal escape, is read back", 0, 45, "/dev/disk one"},
		{"a device number no mount has gives nothing", 0, 99, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mountSource([]byte(mountinfo), tt.major, tt.minor); got != tt.want {
				t.Errorf("mountSource(%d:%d) = %q, want %q", tt.major, tt.minor, got, tt.want)
			}
		})
	}
}
