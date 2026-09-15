# Setting up the DROK USB knob

Sparks can use the DROK USB volume knob (iWit "USB Controller", USB ID
`0483:5742`) as a mappable control, like a MIDI controller. Sparks reads the
knob directly, so it works whichever window has focus and never changes the
PC's volume or types anything.

## One-time setup: give the knob the WinUSB driver

Windows normally treats the knob as a keyboard and volume control. Sparks needs
it on the generic WinUSB driver instead.

1. Download Zadig from https://zadig.akeo.ie and run it.
2. Plug the knob in without holding its button, and don't turn or press it yet.
3. In Zadig choose **Options → List All Devices**.
4. Pick **iWit USB Controller**. Check that **USB ID** reads **0483 5742**.
   If it doesn't, stop: replacing another device's driver can disable your
   keyboard or touchpad.
5. Set the target driver to **WinUSB** and click **Replace Driver**. Answer
   **Yes** to the system driver warning.
6. Unplug the knob and plug it back in.

The knob now does nothing in Windows on its own.

## Map it in Sparks

1. In Settings → MIDI, tick **iWit USB Controller (knob)**. With the knob
   plugged in, it shows no warning.
2. Open **Mappings** and click **+ Add**. Set the row's type to **Knob**.
3. Click **Learn**, then turn or press the knob within 10 seconds. The row
   fills in the gesture (for example "Turn Right").
4. Pick an action:
   - Step actions (VFO Tune, VFO B Tune, RF Gain Adjust, Filter BW Adjust)
     need a **Direction**: map turn right with **+** and turn left with **−**.
   - Button actions (VFO A/B Swap, RIT Clear, Band Up, CW Message, …) run once
     per gesture.
5. Repeat for the other gestures, then **Save Settings** and **Start**.

The knob's six gestures on the author's unit: Turn Right, Turn Left, Press,
Press + Turn Right, Press + Turn Left (two steps per click), Long Press.

## Status

| Device list shows | Meaning |
|---|---|
| no note | connected |
| (not connected) | not plugged in; Sparks keeps retrying while Started |
| (not set up — replace its driver with WinUSB using Zadig) | still on the Windows keyboard driver; run the setup above |
| (Windows only) | this platform can't read the knob |

## Undo

To make it a normal volume knob again: Device Manager → find the knob → right
click → **Uninstall device**, tick **Delete the driver software** if offered,
then unplug and replug it.

If a Windows update puts the old driver back, the device list shows
"not set up"; run the Zadig steps again.
