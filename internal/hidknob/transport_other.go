//go:build !windows

package hidknob

// supported reports whether this platform can read the knob.
const supported = false

func openDevice() (transport, State, error) {
	return nil, StateUnsupported, ErrUnsupported
}
