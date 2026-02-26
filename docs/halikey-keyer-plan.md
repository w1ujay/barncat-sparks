# HaliKey MIDI Paddle + Iambic Keyer Plan

## Context

The Elecraft HaliKey is a USB adapter that presents physical CW paddles and PTT as MIDI Note On/Off events. QK4 has a full integration with an iambic keyer state machine. This plan covers bringing HaliKey support into sparks in two steps.

## Step 1: Direct MIDI Paddle Actions (current implementation)

Map HaliKey MIDI notes to K4 `KZ` (CW element) commands:

- **Note 20** (dit paddle) → `KZ.;` — K4 plays one dit at current WPM
- **Note 21** (dah paddle) → `KZ-;` — K4 plays one dah at current WPM
- **Note 31** (PTT) → `TX;`/`RX;` — existing PTT action works as-is

Each paddle press sends one element. No auto-repeat, no iambic logic. The K4 times each element internally based on its keyer speed setting. Functional for tapping out CW manually.

### Config: Paddle Swap

`paddle_swap` boolean in AppConfig. When true, dit_paddle sends `KZ-;` and dah_paddle sends `KZ.;`. Applied at MIDI dispatch time.

### Changes

- `Radio` interface: add `SendCWElement(dit bool) error`
- `CATClient`: implement as `KZ.;` / `KZ-;`
- `RadioServer`: dispatch KZ commands
- Action registry: `dit_paddle`, `dah_paddle` actions
- MIDI dispatch: send element on note_on, respect paddle_swap
- Config: `paddle_swap` field, web UI checkbox

## Step 2: Iambic Keyer (future)

A software iambic keyer in sparks that turns paddle state into properly timed CW elements.

### Architecture

```go
// internal/keyer/iambic.go

type IambicKeyer struct {
    mu       sync.Mutex
    radio    cat.Radio
    mode     int         // 0=iambic A, 1=iambic B
    wpm      int
    reversed bool        // paddle swap

    ditDown  bool        // current dit paddle state
    dahDown  bool        // current dah paddle state
    squeezed bool        // both paddles were held during element
    sending  bool        // element in progress

    stop     chan struct{}
}
```

### Iambic State Machine

QK4's implementation (reference: `src/hardware/iambickeyer.cpp`):

1. **Idle** — waiting for paddle input
2. **Element** — playing a dit or dah (timer running)
3. **Inter-element gap** — 1 dit-unit silence between elements (handled by K4 via KZ timing)

On timer fire (element complete):
- Both paddles held → alternate (squeeze)
- Opposite paddle held → cross-paddle
- Iambic B + squeeze was active → one extra opposite element
- Nothing held → go idle

### Timing

- Dit duration = `1200 / WPM` ms
- Dah duration = `3 × dit` ms
- Inter-element = 1 dit unit (K4 handles this after KZ command)
- Element timer = `dit * 2` (dit) or `dit * 4` (dah) — includes inter-element

### Integration Points

The iambic keyer would:
- Receive paddle state from MIDI note_on/note_off events
- Run its own timing loop (Go ticker or time.AfterFunc)
- Send `KZ.;` / `KZ-;` to radio at element boundaries
- Optionally generate local sidetone (future)

The MIDI dispatch would change from "send element immediately" to "set paddle state on keyer":
```go
case "dit_paddle":
    d.iambicKeyer.SetDit(noteOn)  // true on note_on, false on note_off
case "dah_paddle":
    d.iambicKeyer.SetDah(noteOn)
```

### Open Questions

1. **KZ straight key mode**: Does the K4's KZ command support a "key down until told to stop" mode, or only timed elements? If it supports key-down/key-up, a straight key mode becomes possible where holding a paddle keys continuously. The K4 programmer's reference should document this. QK4 uses `KZU0000` for key-up which suggests there may be a raw keying mode.

2. **KZ and WinKeyer interaction**: Could KZ commands be generated from the WinKeyer engine instead of (or in addition to) KY? Currently WinKeyer uses `KY` for buffered text. KZ could provide lower-latency character-by-character keying. However, KY handles the full text buffer and the K4's keyer does the timing, so KZ would only help if we wanted sparks to do its own Morse encoding (character → element sequence). Likely not worth it — KY works well for buffered text, KZ is for real-time paddle input.

3. **Latency**: Over TCP to K4, each KZ command has network round-trip latency. At 25 WPM, a dit is 48ms. If TCP latency is >10ms, iambic feel will suffer. Local sidetone (playing audio immediately, before the radio confirms) would mask this. QK4 does exactly this — plays sidetone locally and sends KZ in parallel.

4. **MoMIDI protocol**: HaliKey v2 supports MoMIDI which embeds timing data in CC messages preceding Note events. This could be used to compensate for USB latency (the note tells you it happened N ms ago). Worth implementing in step 2 for tighter timing.

5. **Sidetone**: QK4 generates local sidetone audio. Sparks could do the same using a simple sine wave generator. Cross-platform audio output in Go without CGO is possible via `oto` or similar. Lower priority — the K4's MON output provides sidetone through the headphones.

## Step 3: Future Enhancements

- Straight key mode (if K4 supports continuous KZ keying)
- Local sidetone generation
- MoMIDI timing compensation
- Serial V1.4 mode (read CTS/DSR modem signals directly — alternative to MIDI)
- Paddle state visualization in web UI
- Speed pot (HaliKey doesn't have one, but other MIDI controllers could map a CC to WPM)
