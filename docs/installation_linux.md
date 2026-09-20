# Installing the Gemini Flat Panel Proxy on Linux (e.g. a Raspberry Pi)

[← Back to main readme](../readme.md)

This covers running the proxy on a Linux machine — most commonly a Raspberry Pi sitting
next to the rig — as a background `systemd` service, so it starts automatically and
stays running headless. Any Alpaca-capable astronomy software on the network can then
connect to it, whether that's running on the same Linux box (e.g.
[PINS](https://touch-n-stars.eu/#pins), a Linux port of
[N.I.N.A.](https://nighttime-imaging.eu/)) or on a separate Windows/Mac machine.

> [!CAUTION]
> **New, not yet verified against real hardware.** This project's Windows install path
> is what's actually been tested day to day; the Linux build and this installer are new
> and haven't been exercised against a real Linux host yet. It should work — the proxy
> has no Linux-specific hardware workarounds to get wrong, just a plain serial
> connection over the kernel's normal driver — but if you hit a problem, please open an
> issue.

## Contents

- [1. Find the host's address](#1-find-the-hosts-address)
- [2. Connect over SSH](#2-connect-over-ssh)
- [3. Run the installer](#3-run-the-installer)
- [4. What the installer does](#4-what-the-installer-does)
- [5. Confirm it's running](#5-confirm-its-running)
- [6. Connect from your astronomy software](#6-connect-from-your-astronomy-software)
- [7. Everyday commands](#7-everyday-commands)
- [8. Updating](#8-updating)
- [Setting the serial port directly in the config file](#setting-the-serial-port-directly-in-the-config-file)
- [Installing a dev build](#installing-a-dev-build)
- [Uninstalling](#uninstalling)

## 1. Find the host's address

You need either its IP address or its network name. Consult whatever distribution/image
you're using for its own default hostname (e.g. plain Raspberry Pi OS is typically
`raspberrypi.local`):

```bash
# from another machine on the same network
ping raspberrypi.local   # replace with your host's actual hostname
```

No luck? Check your router's admin page for the device's name or IP instead.

## 2. Connect over SSH

```bash
ssh <user>@<host-ip-or-hostname>
```

**On Windows**, if you'd rather not use the `ssh` command (built into PowerShell/cmd on
modern Windows), [PuTTY](https://www.putty.org/) works too: open PuTTY, enter the host's
IP or hostname under "Host Name", leave the port at `22`, click "Open", then log in when
prompted.

First connection from this machine? SSH will ask you to confirm the host's key
fingerprint — type `yes` to continue.

## 3. Run the installer

One command, once you're logged in. It downloads the release binary matching your
host's architecture, installs it, and sets it up as a service:

```bash
curl -sSL https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/latest/download/install_linux.sh | sudo bash
```

## 4. What the installer does

Five quick steps, all logged to the terminal as they happen:

1. Downloads the binary for your host's CPU (amd64 or arm64)
2. Adds your user to the `dialout` group for serial port access
3. Stops any existing installation's service, if present
4. Installs the binary to `/usr/local/bin`
5. Registers and starts the `geminiflatpanel` systemd service

It finishes by printing the web interface URL — that's both the setup dashboard and
your ASCOM Alpaca endpoint.

## 5. Confirm it's running

```bash
sudo systemctl status geminiflatpanel
```

Look for `active (running)`. Then open the printed URL (port `32300`) in a browser on
the same network to confirm the dashboard loads and shows the connected panel.

## 6. Connect from your astronomy software

Alpaca-capable clients (N.I.N.A., PINS, etc.) should find the proxy automatically via
Alpaca discovery on the local network. If a client doesn't show it, try its manual
"add device by IP" option with the host's address and port `32300`.

## 7. Everyday commands

Keep these handy — no need to memorize, they're printed again at the end of every
install:

| Command | What it does |
|---|---|
| `sudo systemctl status geminiflatpanel` | is it running right now? |
| `sudo journalctl -u geminiflatpanel -f` | watch the live log |
| `sudo systemctl restart geminiflatpanel` | restart the service |
| `sudo systemctl stop geminiflatpanel` | stop it |

## 8. Updating

Re-run the exact same command from step 3 — it downloads whatever's currently latest,
stops the running service, replaces the binary, and starts it back up:

```bash
curl -sSL https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/latest/download/install_linux.sh | sudo bash
```

Your saved settings and calibration are untouched — the installer only ever replaces
the binary and the service definition, never `~/.config/GeminiFlatPanelProxy/`. Confirm
the update landed with `sudo systemctl status geminiflatpanel` (its recent log lines
show the version that just started) or the version shown in the web interface.

## Setting the serial port directly in the config file

The proxy needs to be told which serial port the panel is on — there's no
autodetection. The easiest way is the web dashboard's Settings panel, which offers a
dropdown of enumerated ports (a plain enumeration, refreshable, never opens or resets
a USB serial adapter) instead of requiring you to find the device node by hand. This section
covers setting it directly in the config file instead, useful before the service has
ever run or without network access to the dashboard yet.

1. Stop the service, so nothing is holding the port while you do this:
   ```bash
   sudo systemctl stop geminiflatpanel
   ```
2. Find the panel's device node — with the panel plugged in:
   ```bash
   ls /dev/ttyUSB* /dev/ttyACM* 2>/dev/null
   ```
   If more than one shows up and you're not sure which is the panel, unplug it, note
   what's left, then plug it back in and see what device node newly appears (or check
   `dmesg | tail -n 20` right after plugging it in).
3. Edit `~/.config/GeminiFlatPanelProxy/proxy_config.json` (create the directory/file
   first if the proxy has never run yet) and set:
   ```json
   "serialPortName": "/dev/ttyUSB0",
   ```
   (Use whatever device node you found in step 2 — the example above is illustrative,
   not necessarily correct for your system.)
4. Restart the service and confirm it connected to that port:
   ```bash
   sudo systemctl restart geminiflatpanel
   sudo journalctl -u geminiflatpanel -n 20
   ```

Got the device node wrong, or does it change after a reboot (common with multiple
USB-serial devices, since `/dev/ttyUSB0` vs `/dev/ttyUSB1` assignment isn't guaranteed
stable across reconnects)? Nothing breaks — an unreachable port is simply retried in
the background like any other disconnect. Re-check the device node (step 2) and update
`serialPortName` again, or switch to picking it from the Settings dropdown instead,
which doesn't have this problem since you select the currently-live name each time.

## Installing a dev build

Rebuilt automatically whenever anything changes in the repo (at most once a day),
published as a single rolling
[`dev-build` prerelease](https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/tag/dev-build) —
always whatever's newest. Unreviewed and can be broken at any given moment; for testing
only, not for anything you depend on. Set `GFP_RELEASE_TAG` when installing instead of
using step 3's plain command:

```bash
curl -sSL https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/download/dev-build/install_linux.sh | sudo GFP_RELEASE_TAG=dev-build bash
```

(Note the env var comes right after `sudo`, not before the whole command — `sudo`
doesn't pass through your shell's environment variables otherwise.)

## Uninstalling

Same one-liner, with `GFP_UNINSTALL=1` instead:

```bash
curl -sSL https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/latest/download/install_linux.sh | sudo GFP_UNINSTALL=1 bash
```

Removes the service and the binary. Leaves your `dialout` group membership and any
saved config under `~/.config/GeminiFlatPanelProxy/` in place — other things on the
system may depend on the group, and the config is worth keeping if you reinstall later.
