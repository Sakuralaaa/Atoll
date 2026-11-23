package api

import (
	"path"
	"regexp"
)

// MaxShmSizeMB caps Service.ShmSizeMB (/dev/shm of the container, docker --shm-size).
const MaxShmSizeMB = 65536

// allowedDevices are the host devices a service may map (Service.Devices, docker --device).
// Only devices meant for unprivileged use: KVM for emulators and VMs, TUN for VPNs, FUSE,
// and GPU render nodes. Disks, memory and the rest of /dev are never mapped.
var allowedDevices = regexp.MustCompile(`^/dev/(kvm|net/tun|fuse|dri|dri/renderD[0-9]{1,3}|dri/card[0-9]{1,2})$`)

// AllowedDevices names the devices AllowedDevice accepts, for error messages and the panel.
const AllowedDevices = "/dev/kvm, /dev/net/tun, /dev/fuse, /dev/dri (or /dev/dri/renderD*, /dev/dri/card*)"

// AllowedDevice reports whether a service may map the host device p.
func AllowedDevice(p string) bool {
	return p == path.Clean(p) && allowedDevices.MatchString(p)
}
