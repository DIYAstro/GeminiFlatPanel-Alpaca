# Gemini Flat Panel — ASCOM Alpaca Proxy

A third-party ASCOM Alpaca driver for Gemini Astro's flat-field calibrator panels (Standard/Rev2, Lite, and Pro), with a web dashboard and a Windows system tray app. Auto-detects the connected panel revision and exposes it via the ASCOM Alpaca `CoverCalibrator`/`Switch` APIs to astronomy software such as N.I.N.A. or SGP.

**Why this exists:** Gemini's own official driver splits the panel's brightness into two banks ("low"/"high", each 0–254) — but the bank isn't a live-settable property at all, it's selected in the driver's *configuration dialog*. Switching banks mid-session means disconnecting the ASCOM driver, opening its configuration, changing the bank, saving, and reconnecting. That makes automated flat acquisition across the panel's full brightness range effectively impossible, and even doing it manually is a real stumbling block. This project's central purpose is closing exactly that gap: it presents ASCOM/Alpaca clients with a single continuous 0–510 brightness value and handles the bank switching internally, live, with no reconnect required.

**It also goes further than the official driver in one more way:** Gemini's own driver only ever exposes the Pro panel's dew heater as a manual 0–100% slider. This proxy adds fully automatic control on top of that — a heating curve driven by the live delta between ambient temperature and dew point, so the heater runs itself instead of needing to be babysat and adjusted by hand through the night. For the temperature and dew point it needs either an Alpaca `ObservingConditions` device on your network, or the free Open-Meteo weather service, which requires an internet connection at your observing site.

---

<p align="center">
  <em>This is a hobby project. If you find it useful and would like to support a good cause, consider donating via <strong>betterplace.org</strong>—all donations go directly to <strong>Doctors Without Borders</strong>.</em><br><br>
  <a href="https://www.betterplace.org/de/fundraising-events/55631-diyastro-for-doctors-without-borders">
    <img src="https://img.shields.io/badge/Donate-Doctors%20Without%20Borders-red?style=for-the-badge&logo=heart" alt="Donate to Doctors Without Borders">
  </a>
</p>

---

## ✨ Features

* 🎚️ **Continuous 0–510 brightness** exposed to Alpaca clients, with the low/high bank switch handled internally and live — the whole reason this project exists (see above).
* 🔍 **Auto-detects the connected panel's firmware revision** (Rev2, Lite, Pro) once you point it at the right serial port — no need to know which one your panel runs.
* 🌡️ **Automatic dew-heater control**: a heating curve (or, for a plain on/off switch, a threshold with hysteresis) driven by the delta between ambient temperature and dew point, sourced from an Alpaca `ObservingConditions` device or Open-Meteo (needs an internet connection). Drives the Pro panel's own built-in heater, or a channel on any separate Alpaca `Switch` device instead — the latter also works on Rev2/Lite, which have no heater output of their own. See [docs/configuration.md](docs/configuration.md#automatic-dew-control).
* 🖥️ **Web dashboard** for live status, calibration, and setup — no separate config-only ASCOM dialog to hunt through.
* 📡 **Alpaca UDP discovery**, so clients find the proxy on the network automatically.
* 🪟🐧 **Runs on Windows** (installer, system tray, autostart) **and Linux** (systemd service, e.g. a Raspberry Pi next to the rig).

## 📚 Documentation

**Getting Started**
- 🚀 [Installation (Windows & Linux)](docs/installation.md)
- 🐧 [Linux / Raspberry Pi Install — full walkthrough](docs/installation_linux.md)

**Using the Proxy**
- 📄 [`proxy_config.json` Reference](docs/configuration.md)

**Reference**
- 📡 [Serial Protocol Reference](docs/serial_communication.md)

**Contributing**
- 🏗️ [Compiling from Source](docs/building.md)
- 📦 [Build Scripts & Release Process](build_scripts/readme.md)

**History**
- 📝 [CHANGELOG](CHANGELOG.md)

---

## "As-Is" Warranty Disclaimer

This software is provided **"as is"** and **"with all faults"**, without warranty of any kind. The author makes no representations or warranties of any kind concerning the safety, suitability, lack of viruses, inaccuracies, typographical errors, or other harmful components of this software. There are inherent dangers in the use of any software, and you are solely responsible for determining whether this software is compatible with your equipment and other software installed on your system.

Licensed under the GNU General Public License v3.0 — see [LICENSE](LICENSE).
