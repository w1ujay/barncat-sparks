package main

// listMIDIDeviceNames returns available MIDI device names.
// On non-Windows platforms, returns an empty list.
func listMIDIDeviceNames() []string {
	return enumerateMIDIDevices()
}
