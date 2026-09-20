# ASCOM Driver Registration (classic ASCOM clients)

[← Back to main readme](../readme.md)

Registering an ASCOM driver creates a permanent entry in the ASCOM Chooser that tells ASCOM exactly where the proxy is. Alpaca-capable software such as N.I.N.A. usually finds the proxy on its own through Alpaca discovery, even on the same PC, so this is often not needed. You need it when your astronomy software:

1. has no Alpaca client and only offers the classic ASCOM Chooser, or
2. has an Alpaca client, but its discovery doesn't list the proxy. Typical causes: another Alpaca server on the same machine already occupies the discovery port (UDP 32227), so only one of the two can answer, or a firewall or network setup blocks the discovery packets.

If your software already lists "Gemini Flat Panel" through Alpaca discovery, you can skip this page.

> [!NOTE]
> On Windows the proxy listens on `127.0.0.1` by default (`listenAddress` in the [configuration reference](configuration.md)), so it is only reachable from the same PC, whether through discovery or a registered driver. To use it from another PC, change `listenAddress` first.

There are two ways to register a driver: an automated helper script (recommended) and a manual method.

**Prerequisite:** the [ASCOM Platform](https://ascom-standards.org/) must be installed on the PC that runs your astronomy software.

## Easy: the helper script (Windows)

The Windows installer includes a helper script that does the registration for you.

1. Open the Start Menu and go to the **Gemini Flat Panel** folder.
2. Click **Create Gemini Flat Panel Ascom Driver** and confirm the Windows administrator prompt (UAC). Registering a driver requires administrator rights.
3. Follow the prompts in the window that opens:
   - The script reads the IP address and port from the proxy's `proxy_config.json` and shows them. Press **Enter** to accept, or answer `n` to enter an IP address and port by hand (for example for a proxy running on another PC or a Raspberry Pi). If no configuration is found on this PC, it asks for them directly.
   - It asks for a driver name (default: `Gemini Flat Panel`).
4. When it reports **SUCCESS**, select the new driver by that name in your astronomy software.

You can run the script several times, e.g. to create drivers with different names or for different proxies.

> [!NOTE]
> The helper only creates the **CoverCalibrator** driver (light and cover). The proxy also exposes the dew heater as an Alpaca **Switch** device. If a classic ASCOM client should control that too, register it with the manual method below.

## Manual: ASCOM Diagnostics (fallback)

Use this if the helper script fails, if you also need the Switch driver, or if the proxy runs on another machine and the helper isn't installed on the PC that runs your astronomy software.

1. Start **ASCOM Diagnostics** (Start Menu, folder *ASCOM Platform*).
2. Select the device type, `CoverCalibrator` (or `Switch` for the dew heater), and click **Choose Device...**.
3. In the Chooser window's menu bar click **Alpaca**, then **Create Alpaca Driver (Admin)**, and confirm the administrator prompt.
4. Enter a name for the driver, e.g. `Gemini Flat Panel`, and click **OK**.
5. Your new driver is now selected in the Chooser. Click **Properties...** and enter:
   - **Remote Device Host Name or IP Address:** `localhost` (or the IP address of the machine running the proxy)
   - **Alpaca Port:** `32300` (or the `networkPort` you configured)
   - **Remote Device Number:** `0`
6. Click **OK** in the setup window and in the Chooser. Optionally click **Connect** in ASCOM Diagnostics to test.

Repeat the steps for the second device type if you want both.

## Connect-on-Demand and the connection timeout

If you use [Connect-on-Demand](configuration.md#connect-on-demand), establishing the serial connection can take up to about 15 seconds, but ASCOM's Alpaca driver gives up after 2 seconds by default. The helper script raises this **Establish Connection Timeout** to 20 seconds automatically when it detects that Connect-on-Demand is enabled. For a driver you created manually, raise it to at least 20 seconds yourself in the driver's ASCOM Properties dialog.
