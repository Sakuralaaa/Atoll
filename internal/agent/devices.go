package agent

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
)

// statDevice is os.Stat; tests replace it.
var statDevice = os.Stat

// checkDevice fails a deploy whose device does not exist on this server, with a hint, instead
// of letting Docker fail with "error gathering device information".
func checkDevice(d string) error {
	fi, err := statDevice(d)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if d == "/dev/kvm" {
			return fmt.Errorf("this server has no /dev/kvm: KVM is not available (enable nested virtualization or load the kvm module)")
		}
		return fmt.Errorf("this server has no %s", d)
	case err != nil:
		return fmt.Errorf("device %s: %w", d, err)
	case fi.Mode()&fs.ModeDevice == 0 && !fi.IsDir():
		return fmt.Errorf("%s on this server is not a device", d)
	}
	return nil
}
