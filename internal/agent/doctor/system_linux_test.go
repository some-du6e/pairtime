// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import "testing"

func TestMountSource(t *testing.T) {
	const mountinfo = `22 1 259:2 / / rw,relatime shared:1 - ext4 /dev/nvme0n1p2 rw
35 22 0:32 /@home /home rw,relatime shared:2 - btrfs /dev/nvme0n1p3 rw,ssd,space_cache=v2,subvolid=257
36 22 0:43 / /tmp rw,nosuid,nodev shared:3 - tmpfs tmpfs rw,size=8G
37 22 0:44 / /var/lib/docker/overlay2/x/merged rw,relatime - overlay overlay rw,lowerdir=/l
38 22 0:45 / /mnt/usb\040drive rw,relatime shared:4 - btrfs /dev/disk\040one rw
39 35 0:32 /@cache /home/teen/.cache rw,relatime shared:5 - btrfs /dev/nvme1n1p1 rw
40 22 0:46 / /homework rw,relatime - ext4 /dev/sdb1 rw
not a mountinfo line
`
	tests := []struct {
		name, path, want string
	}{
		{"a folder on a btrfs subvolume, whose files' device number no line lists, is found by its mount point", "/home/teen/.local/share/pairtime", "/dev/nvme0n1p3"},
		{"the longest mount point the folder is in wins over the ones around it", "/home/teen/.cache/x", "/dev/nvme1n1p1"},
		{"a mount point is a whole folder name, so /home doesn't hold /homework", "/homework/pairtime", "/dev/sdb1"},
		{"a folder on no mount but the root's is on the root's disk", "/opt/pairtime", "/dev/nvme0n1p2"},
		{"tmpfs has no disk", "/tmp/pairtime", ""},
		{"overlayfs has no disk of its own", "/var/lib/docker/overlay2/x/merged/data", ""},
		{"a mount point and a source with spaces, which mountinfo writes as octal escapes, are read back", "/mnt/usb drive/pairtime", "/dev/disk one"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := mountSource([]byte(mountinfo), tt.path); got != tt.want {
				t.Errorf("mountSource(%q) = %q, want %q", tt.path, got, tt.want)
			}
		})
	}
}
