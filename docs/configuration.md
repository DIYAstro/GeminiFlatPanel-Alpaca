# Configuration

[← Back to main readme](../readme.md)

The application saves its settings in `proxy_config.json` inside the user profile directory:
* **Windows:** `%APPDATA%\GeminiFlatPanelProxy\proxy_config.json`
* **Linux:** `~/.config/GeminiFlatPanelProxy/proxy_config.json`

## Configuration Schema (`proxy_config.json`)

```json
{
  "listenAddress": "0.0.0.0",
  "networkPort": 32300,
  "serialPortName": "COM3",
  "panelRevision": "auto",
  "logLevel": "INFO",
  "maxBrightness": 510,
  "highBankStartValue": 9,
  "blockLightWhenOpen": false,
  "enableAlpacaDiscovery": true,
  "enableBeep": true,
  "enableNotifications": true,
  "settleTime": 2000,
  "maxConnectionRetries": 3,
  "connectionRetryInterval": 1000,
  "coverTimeout": 60,
  "serialConnectOnDemand": false,
  "idleReleaseTimeoutSeconds": 60,
  "enableAutoDewControl": false,
  "dewControlIntervalMinutes": 5,
  "dewControlDeltaFullPower": 1.0,
  "dewControlDeltaZeroPower": 5.0,
  "dewControlOnlyWhenOpen": false,
  "dewControlFailsafeEnabled": false,
  "dewControlFailsafePercent": 0,
  "observingConditionsUrl": "",
  "observingConditionsDeviceNumber": 0,
  "enableOpenMeteo": false,
  "weatherLatitude": 0,
  "weatherLongitude": 0,
  "dewHeaterBackend": "none",
  "dewHeaterSwitchUrl": "",
  "dewHeaterSwitchDeviceNumber": 0,
  "dewHeaterSwitchId": 0,
  "dewHeaterSwitchIsRheostat": false,
  "dewControlOnOffTargetDelta": 3.0,
  "dewControlOnOffHysteresis": 1.0
}
```

## Parameter Reference

| Parameter | Type | Default | Description |
| :--- | :--- | :--- | :--- |
| `listenAddress` | string | `"127.0.0.1"` (Windows) / `"0.0.0.0"` (Linux) | IP address the HTTP server listens on. Windows defaults to localhost-only for security; Linux defaults to all interfaces, since it's typically run headless (e.g. a Raspberry Pi) and needs to be reachable from other machines on the network. |
| `networkPort` | integer | `32300` | Network port for Alpaca clients and the web dashboard. |
| `serialPortName` | string | `"COM3"` | The serial port the panel is connected to (e.g. `"COM3"` on Windows, `"/dev/ttyUSB0"` on Linux). No autodetection — pick it from the dropdown in the web dashboard's Settings panel (a plain enumeration, never opens/resets a USB serial adapter) or set it here directly. |
| `panelRevision` | string | `"auto"` | Which Gemini Flat Panel firmware to expect on `serialPortName`: `auto`, `rev2` (the original revision), `lite` (no motorized cover), or `pro` (newest, async cover). `auto` tries every known revision's handshake in turn; a specific value skips straight to it. |
| `logLevel` | string | `"INFO"` | Logging verbosity (`DEBUG`, `INFO`, `WARN`, `ERROR`). |
| `maxBrightness` | integer | `510` | The maximum brightness limit. Set to 254 for panels supporting only Low-Bank mode, or 510 for panels supporting both Low-Bank and High-Bank modes. |
| `highBankStartValue`| integer | `9` | The internal high-bank PWM value used right at the low/high transition, so it doesn't visibly jump. See "Calibrating highBankStartValue" below. |
| `blockLightWhenOpen`| boolean | `false` | If true, turns off the calibrator when the cover status is open. |
| `enableAlpacaDiscovery`| boolean | `true` | Enables Alpaca SSDP discovery responder for client auto-discovery. |
| `enableBeep` | boolean | `true` | Enables device sound signal for cover movements. |
| `enableNotifications` | boolean | `true` | Enables system tray OS notifications for connection/status changes. |
| `settleTime` | integer | `2000` | Settle time in milliseconds before reporting success after a brightness change. |
| `maxConnectionRetries` | integer | `3` | Maximum number of serial reconnection attempts before notifying clients of a disconnect. |
| `connectionRetryInterval` | integer | `1000` | Delay in milliseconds between serial reconnection attempts. |
| `coverTimeout` | integer | `60` | Maximum time, in seconds, to wait for the cover to finish opening or closing before reporting its ASCOM `CoverState` as `Unknown`. |
| `serialConnectOnDemand` | boolean | `false` | When true, the port is left untouched until an Alpaca client connects or the dashboard's Connect button is used, instead of retrying in the background at all times. See "Connect-on-Demand" below. |
| `idleReleaseTimeoutSeconds` | integer | `60` | Only relevant when `serialConnectOnDemand` is true: how long to hold the port open after an Alpaca client disconnects before releasing it, in case of a quick reconnect. See "Connect-on-Demand" below. |
| `enableAutoDewControl` | boolean | `false` | Enables the automatic heating-curve/threshold loop. Only takes effect once `dewHeaterBackend` is also set to something other than `"none"`. See "Automatic Dew Control" below. |
| `dewControlIntervalMinutes` | integer | `5` | How often the auto-control loop re-checks the weather data and recomputes heater power. |
| `dewControlDeltaFullPower` | float | `1.0` | Temperature-minus-dew-point delta (°C) at or below which the heater runs at 100%. |
| `dewControlDeltaZeroPower` | float | `5.0` | Delta (°C) at or above which the heater turns off. Must be greater than `dewControlDeltaFullPower`. |
| `dewControlOnlyWhenOpen` | boolean | `false` | When true, the heater is forced to 0% on every tick unless the cover is currently confirmed `Open` — see "Automatic Dew Control" below. |
| `dewControlFailsafeEnabled` | boolean | `false` | When true, forces the heater to `dewControlFailsafePercent` on any tick with no weather data available, instead of leaving it at its last value. See "Automatic Dew Control" below. |
| `dewControlFailsafePercent` | integer | `0` | Heater power (0-100) forced when the failsafe above applies. |
| `observingConditionsUrl` | string | `""` | Base URL of an external Alpaca `ObservingConditions` device to read Temperature/DewPoint from, e.g. `"http://192.168.1.50:11111"`. Empty = not configured, falls through to Open-Meteo. |
| `observingConditionsDeviceNumber` | integer | `0` | Alpaca device number at that URL (almost always `0`). |
| `enableOpenMeteo` | boolean | `false` | Enables the Open-Meteo fallback data source. An explicit flag rather than inferring "configured" from the coordinates below being non-zero, which would otherwise misfire for a real location at exactly `0,0`. |
| `weatherLatitude` / `weatherLongitude` | float | `0` | Coordinates used to query Open-Meteo when `enableOpenMeteo` is `true`. |
| `dewHeaterBackend` | string | `"none"` | What the auto-control loop actually drives: `"none"` (feature disabled, dashboard card hidden), `"built-in"` (the connected Pro panel's own heater output), or `"external"` (a channel on a separate Alpaca `Switch` device — the only option that works on a Rev2/Lite panel, which has no heater output of its own). |
| `dewHeaterSwitchUrl` / `dewHeaterSwitchDeviceNumber` / `dewHeaterSwitchId` | string / integer / integer | `""` / `0` / `0` | The external Switch device and channel to drive. Only meaningful when `dewHeaterBackend` is `"external"`. |
| `dewHeaterSwitchIsRheostat` | boolean | `false` | Whether the selected external channel is a PWM-style rheostat (`true`, driven by the same curve as the built-in heater) or a plain boolean on/off outlet (`false`, driven by `dewControlOnOffTargetDelta`/`dewControlOnOffHysteresis` instead). Detected automatically from the channel's own `MinSwitchValue`/`MaxSwitchValue`/`SwitchStep` when the channel is selected in the Dew Control Setup dialog — not meant to be hand-edited. |
| `dewControlOnOffTargetDelta` / `dewControlOnOffHysteresis` | float | `3.0` / `1.0` | For an external on/off channel: the switch turns on once the delta drops to `dewControlOnOffTargetDelta - dewControlOnOffHysteresis` and off once it rises back above `dewControlOnOffTargetDelta + dewControlOnOffHysteresis`, holding its last state in between. The hysteresis band prevents rapid on/off cycling near the threshold. |

## Connect-on-Demand

By default, the proxy keeps trying to reconnect to the panel in the background at all
times (every few seconds) whenever it isn't connected — useful for the common case
where the panel is plugged in for the whole session, since the dashboard and any Alpaca
client can rely on it already being connected. If the panel is only plugged in
occasionally, though, this means constant reconnect attempts (and log entries — the log
file only rotates on process restart, not by size, so a long-running proxy with the
panel absent for days can accumulate a meaningful amount of log data) for as long as it
stays unplugged.

Setting `serialConnectOnDemand: true` replaces this with a dormant-until-asked model:
the port isn't touched at startup or in the background at all. It's only opened when:
- An Alpaca client PUTs `Connected=true` (the request itself takes as long as the
  actual handshake, up to ~15 seconds, rather than failing immediately if the
  background loop hadn't already connected on its own — this mode makes that PUT
  actively try instead of just checking).
- The dashboard's **Connect** button is used (shown next to the connection status pill
  whenever this mode is on and nothing is currently connected).

When an Alpaca client disconnects (`Connected=false`), the port isn't released
immediately — it's held open for `idleReleaseTimeoutSeconds` (default 60 seconds) first,
in case of a quick reconnect (e.g. an equipment profile switch in the client software),
then released if nothing reconnected in that window.

## Automatic Dew Control

A dew heater can be driven automatically from a heating curve, instead of only ever
being set manually. `dewHeaterBackend` selects what's actually driven:
- `"built-in"` — the connected Pro panel's own heater output.
- `"external"` — a channel on a separate Alpaca `Switch` device (any Alpaca-compliant
  switch/relay product works, not just a specific one). This is the only option that
  works on a Rev2/Lite panel, which has no heater output of its own — pick a device and
  channel via the dashboard's Dew Control Setup dialog, which also broadcasts a
  standard Alpaca UDP discovery request to find candidates on the network. Whether the
  selected channel is a PWM-style rheostat or a plain boolean on/off outlet is detected
  automatically from its own `MinSwitchValue`/`MaxSwitchValue`/`SwitchStep` (the same
  rule this proxy's own heater switch uses) and drives which of the two control modes
  below applies.
- `"none"` (default) — the feature is off and the dashboard's Dew Heater card stays
  hidden entirely.

For a rheostat (the built-in heater, or an external PWM channel), the curve is a simple
linear ramp based on the delta between ambient temperature and dew point: it runs at
100% once that delta is at or below `dewControlDeltaFullPower`, at 0% once it's at or
above `dewControlDeltaZeroPower`, and linearly in between (rounded to the nearest 10,
matching the heater's own step size). For an external boolean on/off channel, a
hysteresis band is used instead (see `dewControlOnOffTargetDelta`/
`dewControlOnOffHysteresis` above) — a single threshold would rapidly cycle a relay
near the boundary on realistically noisy temperature readings. Both modes are
recomputed every `dewControlIntervalMinutes`. While Auto mode is on, the dashboard's
Dew Heater card also shows the ambient temperature and dew point from the most recent
successful tick.

**Data sources**, tried in this order every tick:
1. An Alpaca `ObservingConditions` client, if `observingConditionsUrl` is set — any
   Alpaca-compliant weather/environment device works, not just a specific product. Per
   the ASCOM spec, `Temperature` and `DewPoint` are each independently optional
   properties (a device could implement one without the other), so this is checked live
   against the actual device — use the "Test Connection" button in the dashboard's Dew
   Control Setup dialog before relying on it. There's also a "Discover on Network"
   button there that broadcasts a standard Alpaca UDP discovery request (the same
   protocol this proxy itself answers for CoverCalibrator/Switch discovery) and lists
   every ObservingConditions device found, so the URL/device number usually don't need
   typing by hand at all.
2. [Open-Meteo](https://open-meteo.com) (a free, keyless weather API that needs an
   internet connection at your observing site), used only if
   ObservingConditions is unconfigured or its fetch fails, and only if
   `enableOpenMeteo` is `true`.

**Only heat while open**: setting `dewControlOnlyWhenOpen: true` gates the whole curve
behind the cover's ASCOM `CoverState` — on any tick where it isn't strictly `Open`
(closed, moving, or unknown all count as "not open"), the heater is forced off instead
of following the curve. Off by default. Applies to both backends: a Rev2 panel using an
external heater still has a real motorized cover, so this gate remains meaningful there
too (only Lite, which has no cover at all, never engages it).

**Manual overrides are transient by design**: setting the heater manually (dashboard
slider, this proxy's own Alpaca Switch device, the `SetHeaterPower` Action, or the
custom REST endpoint) while Auto mode is on applies immediately, but the next scheduled
tick recomputes the curve and overwrites it again — there's no separate "pause auto
mode" step. The dashboard slider works this way for the built-in heater and for an
external rheostat channel alike (same underlying dispatch); an external boolean on/off
channel has no manual control yet, since there's no single "power" value a slider could
represent. If neither data source is available on a given tick (both
unreachable/unconfigured), that tick is simply skipped and, by default, the heater is
left at whatever it was last set to; it resumes automatically as soon as a source
becomes available again. Setting `dewControlFailsafeEnabled: true` changes that:
instead of leaving the heater at its last value, a data-less tick forces it to
`dewControlFailsafePercent` (default `0`, i.e. off/0%) — useful so a lost data source
can't leave the heater running indefinitely at whatever it happened to be set to last.
For an external on/off channel, the same field is reused as a boolean (`> 0` = force
on) rather than adding a second failsafe field just for that case.

## Calibrating `highBankStartValue`

Panels that support low/high-bank brightness switching (see the [readme](../readme.md)
for why this project exists at all) expose two internal PWM ranges: the low bank,
`0-254`, and the high bank, its own separate `0-255` range. This proxy presents both as
a single continuous `0-510` value to ASCOM/Alpaca clients: `0-254` maps straight onto
the low bank, and `255-510` maps onto the high bank — but not starting at the high
bank's own `0`. Instead it starts at `highBankStartValue` and linearly interpolates up
to `255` as the client value goes from `255` to `510`:

```
internal = highBankStartValue + (client - 255) * (255 - highBankStartValue) / 255
```

The reason for the offset: the two banks are very likely different electrical paths
(not just "the same LED, further up the same curve"), so the low bank's brightness at
254 and the high bank's brightness at its own `0` are not remotely close — sending the
high bank's raw `0` right after the low bank's `254` would produce a large, visible
jump. `highBankStartValue` is the internal high-bank value that continues smoothly from
where the low bank left off, and it's a per-panel calibration value: it depends on your
specific panel's own PWM/brightness relationship, so the packaged default is a
reasonable starting point, not a guarantee for your unit.

**Does the jump actually matter?** For automated flats via something like N.I.N.A.'s
Flat Wizard, not much — it searches for the target brightness adaptively (test
exposure, measure, adjust, repeat), so it doesn't assume linearity and copes fine even
with an uncalibrated offset, just costing a couple of extra iterations. It matters more
if you want to dial in a specific brightness value directly without an adaptive search.

**How to measure it**, using your actual imaging camera rather than an external light
meter (this accounts for the camera's own spectral response, which is what actually
matters for flats):

1. Pick a fixed exposure time and gain — whatever you'd normally use for flats.
2. Take an exposure at low-bank value `254` (`Brightness=254`) and note the mean ADU
   (histogram in N.I.N.A./PixInsight/etc. is enough).
3. Take a couple of exposures well inside the high bank (e.g. `Brightness=280` and
   `Brightness=350`) and note their ADU too.
4. Fit a line through those two high-bank points (`ADU = slope × internal + intercept`,
   where `internal` is what `computeBankedBrightness` would actually send — work it out
   from the formula above, or just watch the proxy's debug log) and solve for the
   `internal` value that would produce the ADU you measured at `254`. That's your
   `highBankStartValue`.

**Worked example**, from real measurements against a Pro panel (ATR2600C camera, 0.1s
exposures, gain 100):

| Brightness | Bank | Internal value sent | ADU |
| :--- | :--- | :--- | :--- |
| 150 | low | 150 | 6,550 |
| 225 | low | 225 | 9,861 |
| 262 | high (offset 9) | 15 | 16,603 |
| 272 | high (offset 9) | 25 | 26,438 |
| 281 | high (offset 9) | 34 | 35,305 |

Low-bank slope ≈ 44.1 ADU/unit, extrapolated to `254` ≈ 11,141 ADU. High-bank slope
(from the 15→25→34 points, internally consistent to within ~0.2%) ≈ 984 ADU/unit.
Solving for the internal value that would give ~11,141 ADU: ≈ 9.4 — hence
`highBankStartValue = 9` as this project's default. Two independent measurement
sessions (at offset `10` and offset `9`) both converged on the same ~9.4 estimate,
which is a good sign of measurement repeatability — but it's still specific to that one
panel. If your own flats consistently need a brightness value right around the `255`
boundary and the exposure doesn't look continuous with the low bank, it's worth
re-measuring for your unit using the steps above.
