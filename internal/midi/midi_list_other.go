//go:build !windows

package midi

func EnumerateMIDIDevices() []string {
	return nil
}
