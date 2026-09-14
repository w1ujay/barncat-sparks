//go:build windows

package hidknob

// supported reports whether this platform can read the knob.
// Task 5 replaces this stub with the WinUSB transport.
const supported = false

func openDevice() (transport, State, error) {
	return nil, StateUnsupported, ErrUnsupported
}
