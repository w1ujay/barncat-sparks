//go:build !windows

package main

func enumerateMIDIDevices() []string {
	return nil
}
