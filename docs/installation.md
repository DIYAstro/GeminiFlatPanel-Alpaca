# Installation

[← Back to main readme](../readme.md)

## Windows
1. Download the latest `GeminiFlatPanelProxy-Setup-vX.Y.Z.exe` from the releases.
2. Run the installer. It terminates running instances, installs the files, and registers the app to run at startup if selected.
3. Search for "Create Gemini Flat Panel Ascom Driver" in the Start Menu (or run `Helper/Create-Driver.bat` from the installation directory as Administrator) to register the driver in the Windows ASCOM registry.
4. Select "Gemini Flat Panel" in the astronomy software.

## Linux
One command, run on the Linux machine (e.g. a Raspberry Pi) with the panel plugged in — downloads the release binary for your architecture and sets it up as a systemd service:
```bash
curl -sSL https://github.com/DIYAstro/GeminiFlatPanel-Alpaca/releases/latest/download/install_linux.sh | sudo bash
```
See [docs/installation_linux.md](installation_linux.md) for the full walkthrough (finding/connecting to the host, what the installer does, everyday commands, updating, uninstalling). There's no ASCOM driver registration step on Linux — Alpaca clients (N.I.N.A., PINS, etc.) discover the proxy over the network directly.
