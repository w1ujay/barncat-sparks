package cat

// Radio defines the interface for controlling a radio via CAT commands.
// All consumers (keyer, midi, webui) depend on this interface rather than
// the concrete CATClient, enabling support for different radio models.
type Radio interface {
	Close()

	// CW keying
	SendKY(text string) error
	SendAbort() error
	SendCWMessage(text string) error
	SendCWElement(dit bool) error // KZ dit/dah element

	// Keyer speed
	SetSpeed(wpm int) error
	QuerySpeed() (int, error)

	// PTT
	SetPTT(on bool) error

	// VFO frequency
	SetFreqA(hz int) error
	SetFreqB(hz int) error
	GetFreqA() (int, error)
	GetFreqB() (int, error)

	// VFO selection
	SelectVFO(ab int) error
	SwapVFO() error
	VFOAtoB() error
	VFOBtoA() error

	// Band
	BandUp() error
	BandDown() error

	// Audio
	SetVolume(recv, level int) error
	SetRFGain(recv, level int) error
	ToggleRFGain(recv int) error
	SetPower(tenths int) error

	// RIT / XIT
	SetRIT(on bool) error
	ClearRIT() error
	SetXIT(on bool) error

	// DSP filters
	SetNB(on bool) error
	SetNR(on bool) error
	SetSplit(on bool) error
	SetFilterBW(hz int) error
	SetNotch(on bool) error
	SetNotchFreq(hz int) error

	// Mode
	SetDataMode() error
	SetCWMode() error
	QueryMode() (int, error)

	// RTTY
	SendRTTYText(text string) error
	SendRTTYEOT() error

	// Text decode
	SetTextDecode(on bool) error
	QueryDecode() (string, error)

	// Raw sends a command the interface doesn't model, without its
	// semicolon. A bare query (two letters, optionally '$') returns the
	// radio's reply without the semicolon; anything else returns "".
	Raw(cmd string) (string, error)
}

// Compile-time check: *CATClient implements Radio.
var _ Radio = (*CATClient)(nil)
