# Sparks on the k3-site Pi (remote K3)

The Pi runs Sparks headless next to the K3 and serves K4-compatible CAT on
the tailnet. Home Sparks connects to it as if it were a K4 (plain TCP,
`<pi-tailnet-ip>:9400`); N1MM stays set to K4.

## Build

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o barncat-sparks-arm64 ./cmd/barncat-sparks/
```

`CGO_ENABLED=0` is required on WSL, where CGO is on by default. Release tags
also publish a `linux_arm64` tarball.

## Install

```bash
scp barncat-sparks-arm64 admin@<pi-tailnet-ip>:/tmp/barncat-sparks
scp deploy/barncat-sparks.service admin@<pi-tailnet-ip>:/tmp/
ssh admin@<pi-tailnet-ip> 'sudo install -D -m 755 /tmp/barncat-sparks /opt/barncat-sparks/barncat-sparks &&
  sudo install -m 644 /tmp/barncat-sparks.service /etc/systemd/system/ &&
  sudo systemctl daemon-reload && sudo systemctl enable --now barncat-sparks'
ssh admin@<pi-tailnet-ip> 'journalctl -u barncat-sparks -n 20 --no-pager'
```

`admin` must be in `dialout`. The service listens only on the tailnet
address. If the USB-serial adapter drops, Sparks exits and systemd restarts
it every 5 s until the port is back. Home Sparks does not reconnect by
itself yet: press Stop/Start after the Pi restarts or the link drops.

To find commands that need a K3 policy, add `-radio-log-raw` to `ExecStart`
and watch `journalctl -u barncat-sparks -f` during an N1MM session.

## On-air checklist (dummy load)

1. N1MM (as K4) shows and follows the K3's frequency and mode; bandmap clicks QSY the rig.
2. With N1MM polling and knob tuning active, a 200-character CW macro at 50 WPM sounds clean and complete. Esc mid-macro stops it. A speed change mid-macro takes effect.
3. 60 WPM from N1MM ends up as 50 (a long macro at 60 may lose its tail).
4. N1MM mode changes: USB, LSB, DATA, CW-REV, DATA-REV.
5. Knob/MIDI: tuning, AF and RF gain match the expected K3 values, band up/down, NB/NR/notch (with NR turned on at the radio first), RIT/XIT, split. Press NR quickly several times and from two clients: one transition per distinct request, no reversal.
6. RTTY in FSK-D: a 200-character exchange goes out complete; abort stops it; the normal `]` release doesn't cut the tail while polling and tuning are active.
7. Hearth powers the K3 off and on: readback blanks, then recovers.
8. `sudo systemctl restart barncat-sparks`: home needs a manual Stop/Start.

Items 2 and 6 are a release gate: if either fails at 50 WPM or normal RTTY
speed, revise the KY flow control before relying on it.
