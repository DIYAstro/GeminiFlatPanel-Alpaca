import { defineStore } from 'pinia'
import { ref } from 'vue'

export const useDeviceStore = defineStore('device', () => {
    const firmwareVersion = ref('-')
    const comPort = ref('-')
    const isConnected = ref(false)
    const connectionStatus = ref('Disconnected')
    // True only while deliberately dormant under connect-on-demand mode (not connected,
    // but by design rather than because a connection attempt is failing) -- lets the
    // header show a distinct "paused" indicator instead of looking like an error.
    const isIdleOnDemand = ref(false)
    const proxyVersion = ref('')
    const liveStatus = ref({
        brightness: 0,
        cover_state: 4, // Unknown
        light_state: 0,
        high_mode: false,
        current_motor_steps: 0,
        open_motor_limit: 0,
        panel_revision: '-',
        // Defaults to false, like has_heater below -- so before the first real
        // /status response arrives (e.g. right after a page reload) neither
        // card renders yet, rather than Cover Control flashing on for every
        // model (including Lite, which doesn't have one) until we actually
        // know better.
        has_cover: false,
        has_beep: true,
        has_brightness_mode: true,
        effective_max_brightness: 510,
        has_heater: false,
        heater_percent: 0,
        has_auto_calibrate: false,
        auto_dew_control_enabled: false,
        dew_control_last_source: '',
        dew_control_temperature: undefined,
        dew_control_dew_point: undefined
    })
    const availableIps = ref([])
    const proxyConfig = ref({})
    const showCalibrationModal = ref(false)

    async function fetchProxySettings() {
        try {
            const response = await fetch('/api/v1/settings')
            if (response.ok) {
                const settings = await response.json()
                if (settings.proxy_config) proxyConfig.value = settings.proxy_config
                if (settings.available_ips) availableIps.value = settings.available_ips
            }
        } catch (e) {
            console.error("Failed to fetch proxy settings", e)
        }
    }

    async function saveProxyConfig(newSettings) {
        try {
            const response = await fetch('/api/v1/settings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(newSettings)
            })
            if (!response.ok) throw new Error(response.statusText)
            checkConnection()
            return true
        } catch (e) {
            console.error("Failed to save proxy config", e)
            throw e
        }
    }

    async function setBrightness(val) {
        try {
            if (val > 0) {
                const formData = new URLSearchParams()
                formData.append('Brightness', val)
                await fetch('/api/v1/covercalibrator/0/calibratoron', {
                    method: 'PUT',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: formData
                })
            } else {
                await fetch('/api/v1/covercalibrator/0/calibratoroff', {
                    method: 'PUT'
                })
            }
            setTimeout(fetchLiveStatus, 200)
        } catch (e) {
            console.error("Error setting brightness:", e)
        }
    }

    async function openCover() {
        try {
            await fetch('/api/v1/covercalibrator/0/opencover', { method: 'PUT' })
            setTimeout(fetchLiveStatus, 200)
        } catch (e) { console.error(e) }
    }

    async function closeCover() {
        try {
            await fetch('/api/v1/covercalibrator/0/closecover', { method: 'PUT' })
            setTimeout(fetchLiveStatus, 200)
        } catch (e) { console.error(e) }
    }

    // haltCover interrupts an in-progress Open/Close -- Pro only (has_halt), see
    // serial.HaltCover's doc comment for what it actually does and its one caveat
    // (cover state reports "Unknown" until a fresh, uninterrupted Open/Close resolves
    // it, rather than possibly-wrong stale info).
    async function haltCover() {
        try {
            await fetch('/api/v1/covercalibrator/0/haltcover', { method: 'PUT' })
            setTimeout(fetchLiveStatus, 200)
        } catch (e) { console.error(e) }
    }

    async function jogMotor(angle) {
        try {
            await fetch('/api/custom/jog', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ angle })
            })
        } catch (e) { console.error(e) }
    }

    async function setHeaterPower(percent) {
        try {
            await fetch('/api/custom/heater', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ percent })
            })
            setTimeout(fetchLiveStatus, 200)
        } catch (e) { console.error(e) }
    }

    async function testObservingConditions(url, deviceNumber) {
        try {
            const response = await fetch('/api/custom/test_observing_conditions', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ url, deviceNumber })
            })
            if (!response.ok) throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            return await response.json()
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    async function discoverObservingConditions() {
        try {
            const response = await fetch('/api/custom/discover_observing_conditions')
            if (!response.ok) throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            const data = await response.json()
            return data.devices || []
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    // Lists serial ports the OS/driver currently reports -- pure enumeration, never
    // opens or resets a USB serial adapter (see internal/config's SerialPortName doc
    // comment for why: autodetect used to, and could reset an unrelated device sharing
    // the same common USB-serial chip as the panel).
    async function listSerialPorts() {
        try {
            const response = await fetch('/api/custom/list_serial_ports')
            if (!response.ok) throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            const data = await response.json()
            return data.ports || []
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    async function discoverDewHeaterSwitches() {
        try {
            const response = await fetch('/api/custom/discover_dew_heater_switches')
            if (!response.ok) throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            const data = await response.json()
            return data.devices || []
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    // Also serves as the "test connection" step: a successful call already proves the
    // device is reachable and the Switch API works, so no separate test endpoint exists.
    async function listSwitchChannels(url, deviceNumber) {
        try {
            const response = await fetch('/api/custom/list_switch_channels', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ url, deviceNumber })
            })
            if (!response.ok) throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            return await response.json()
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    // For connect-on-demand mode's manual "Connect" button (only shown/meaningful when
    // proxyConfig.serialConnectOnDemand is on) -- actively attempts a connection and
    // waits for the result (the request itself takes as long as the actual handshake,
    // up to ~15s), same action an Alpaca client's own Connected=true PUT triggers.
    async function manualConnect() {
        try {
            const response = await fetch('/api/custom/connect', { method: 'POST' })
            if (!response.ok) throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            return await response.json()
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    // The manual counterpart to manualConnect, for the same "Connect" button's
    // opposite state -- releases the port back to dormant. Without this, a connection
    // started via the Connect button (rather than an Alpaca client, which already gets
    // the idle-release grace period on its own Connected=false) had no way back to
    // dormant at all.
    async function manualDisconnect() {
        try {
            const response = await fetch('/api/custom/disconnect', { method: 'POST' })
            if (!response.ok) throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            return await response.json()
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    async function saveClosedPosition() {
        try {
            const response = await fetch('/api/custom/set_closed', { method: 'POST' })
            if (!response.ok) {
                throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            }
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    async function saveOpenedPosition() {
        try {
            const response = await fetch('/api/custom/set_opened', { method: 'POST' })
            if (!response.ok) {
                throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            }
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    // Experimental (Pro only, unconfirmed) - takes ~22s+ on real hardware, the fetch
    // simply waits for the full round trip.
    async function autoCalibrateClosed() {
        try {
            const response = await fetch('/api/custom/auto_calibrate_closed', { method: 'POST' })
            if (!response.ok) {
                throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            }
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    async function autoCalibrateOpen() {
        try {
            const response = await fetch('/api/custom/auto_calibrate_opened', { method: 'POST' })
            if (!response.ok) {
                throw new Error(`Server returned status ${response.status}: ${response.statusText}`)
            }
        } catch (e) {
            console.error(e)
            throw e
        }
    }

    async function fetchLiveStatus() {
        try {
            const response = await fetch('/api/v1/status')
            if (response.ok) {
                liveStatus.value = await response.json()
            }
        } catch (e) {
            console.error("Failed to fetch live status", e)
        }
    }

    async function fetchProxyVersion() {
        try {
            const response = await fetch('/api/v1/proxy/version')
            if (response.ok) {
                const data = await response.json()
                proxyVersion.value = data.version ? `(${data.version})` : ''
            }
        } catch (e) {}
    }

    async function fetchFirmwareVersion() {
        try {
            const response = await fetch('/api/v1/firmware/version')
            if (response.ok) {
                const data = await response.json()
                firmwareVersion.value = data.version || 'Unknown'
            }
        } catch (e) {}
    }

    async function checkConnection() {
        try {
            const response = await fetch('/api/v1/settings')
            if (!response.ok) {
                isIdleOnDemand.value = false
                isConnected.value = false
                connectionStatus.value = "Disconnected"
                comPort.value = "Proxy Offline"
                return
            }

            const settings = await response.json()
            const proxyConf = settings.proxy_config

            if (proxyConf) proxyConfig.value = proxyConf
            if (settings.available_ips) availableIps.value = settings.available_ips

            if (proxyConf && !settings.serial_port_connected && proxyConf.serialConnectOnDemand) {
                // Deliberately dormant, not failing -- distinct from "Connecting...",
                // which would wrongly imply an attempt is underway.
                isConnected.value = false
                isIdleOnDemand.value = true
                connectionStatus.value = "Idle (on demand)"
                comPort.value = "Not Connected"
            } else if (proxyConf && proxyConf.serialPortName) {
                isIdleOnDemand.value = false
                if (settings.serial_port_connected) {
                    isConnected.value = true
                    connectionStatus.value = "Connected"
                    comPort.value = proxyConf.serialPortName
                } else {
                    isConnected.value = false
                    connectionStatus.value = "Connecting..."
                    comPort.value = `Connecting to ${proxyConf.serialPortName}...`
                }
            } else {
                // No serialPortName configured at all -- there is no autodetect to
                // fall back to, this just means nobody has picked a port in Settings
                // yet.
                isIdleOnDemand.value = false
                isConnected.value = false
                connectionStatus.value = "No port configured"
                comPort.value = "Not configured"
            }
        } catch (error) {
            isIdleOnDemand.value = false
            isConnected.value = false
            connectionStatus.value = "Disconnected"
            comPort.value = "Proxy Offline"
        }
    }

    function startPolling() {
        fetchProxyVersion()
        fetchFirmwareVersion()
        checkConnection()

        setInterval(() => {
            checkConnection()
            if (isConnected.value) {
                fetchFirmwareVersion()
                fetchLiveStatus()
            }
        }, 2000)
    }

    return {
        firmwareVersion, comPort, isConnected, connectionStatus, isIdleOnDemand, proxyVersion,
        liveStatus, availableIps, proxyConfig, showCalibrationModal,
        fetchProxySettings, saveProxyConfig,
        setBrightness, openCover, closeCover, haltCover, jogMotor, setHeaterPower, testObservingConditions, discoverObservingConditions, discoverDewHeaterSwitches, listSwitchChannels, listSerialPorts, saveClosedPosition, saveOpenedPosition,
        autoCalibrateClosed, autoCalibrateOpen, manualConnect, manualDisconnect,
        startPolling, checkConnection
    }
})
