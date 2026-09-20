<script setup>
import { ref, onMounted, computed, watch } from 'vue'
import { useDeviceStore } from '../stores/device'
import { useModalStore } from '../stores/modal'

const store = useDeviceStore()
const modal = useModalStore()

const formData = ref({
    listenAddress: '0.0.0.0',
    networkPort: 8080,
    serialPortName: '',
    panelRevision: 'auto',
    maxBrightness: 510,
    highBankStartValue: 9,
    enableAlpacaDiscovery: true,
    blockLightWhenOpen: false,
    enableBeep: true,
    enableNotifications: true,
    logLevel: 'INFO',
    settleTime: 2000,
    maxConnectionRetries: 3,
    connectionRetryInterval: 1.0,
    coverTimeout: 60,
    serialConnectOnDemand: false,
    idleReleaseTimeoutSeconds: 60,
    dewHeaterBackend: 'none'
})

const isSaving = ref(false)
const hasChanges = ref(false)

const originalData = ref(null)
const isCollapsed = ref(false)

// Serial port dropdown -- pure enumeration (see listSerialPorts' own doc comment),
// never opens/resets a USB serial adapter, so refreshing freely (on mount, and via the
// button) is always safe.
const availablePorts = ref([])
const isRefreshingPorts = ref(false)

async function refreshSerialPorts() {
    isRefreshingPorts.value = true
    try {
        availablePorts.value = await store.listSerialPorts()
    } catch (e) {
        // Non-fatal: the dropdown just stays empty/stale, the currently-saved value
        // (if any) is still shown and kept via the synthesized option below.
    } finally {
        isRefreshingPorts.value = false
    }
}

onMounted(async () => {
    await store.fetchProxySettings()
    initFormData()
    refreshSerialPorts()
    const savedState = localStorage.getItem('collapsed-configuration')
    if (savedState === 'true') {
        isCollapsed.value = true
    }
})

function toggleCollapse() {
    isCollapsed.value = !isCollapsed.value
    localStorage.setItem('collapsed-configuration', isCollapsed.value)
}

watch(() => store.proxyConfig, () => {
    if (!hasChanges.value) {
        initFormData()
    }
}, { deep: true })

function initFormData() {
    if (store.proxyConfig && Object.keys(store.proxyConfig).length > 0) {
        formData.value = { ...store.proxyConfig }
        formData.value.connectionRetryInterval = (store.proxyConfig.connectionRetryInterval !== undefined ? store.proxyConfig.connectionRetryInterval : 1000) / 1000
        formData.value.coverTimeout = store.proxyConfig.coverTimeout !== undefined ? store.proxyConfig.coverTimeout : 60
        originalData.value = JSON.stringify(formData.value)
        hasChanges.value = false
    }
}

watch(formData, () => {
    if (originalData.value) {
        hasChanges.value = JSON.stringify(formData.value) !== originalData.value
    }
}, { deep: true })

// The currently-configured port might not be in the freshly-enumerated list (e.g. the
// panel is unplugged right now, or hasn't been scanned yet on first load) -- shown as
// its own option anyway so saving the form never silently discards it.
const portOptions = computed(() => {
    const names = availablePorts.value.map(p => p.name)
    if (formData.value.serialPortName && !names.includes(formData.value.serialPortName)) {
        return [{ name: formData.value.serialPortName, isUsb: false, stale: true }, ...availablePorts.value]
    }
    return availablePorts.value
})

async function saveSettings() {
    isSaving.value = true
    try {
        const portChanged = store.proxyConfig && store.proxyConfig.networkPort !== formData.value.networkPort
        const addressChanged = store.proxyConfig && store.proxyConfig.listenAddress !== formData.value.listenAddress

        const settingsToSave = {
            ...formData.value,
            connectionRetryInterval: Math.max(100, Math.round(parseFloat(formData.value.connectionRetryInterval) * 1000))
        }

        await store.saveProxyConfig(settingsToSave)
        originalData.value = JSON.stringify(formData.value)
        hasChanges.value = false

        if (portChanged || addressChanged) {
            modal.show({
                icon: '⚠️',
                title: 'Restart Required',
                message: 'Settings saved successfully! Since you changed the API Port or Listen Address, you must restart the proxy application for these changes to take effect.'
            })
        } else {
            modal.success('Settings saved successfully.')
        }
    } catch (e) {
        modal.error('Failed to save settings: ' + e.message)
    } finally {
        isSaving.value = false
    }
}
</script>

<template>
  <div class="panel glass-panel settings-container" :class="{ collapsed: isCollapsed }">
    <div class="collapsible-header" :class="{ collapsed: isCollapsed }" @click="toggleCollapse">
      <h2><i class="ri-settings-3-line"></i> Configuration</h2>
      <span class="toggle-icon" :class="{ collapsed: isCollapsed }"></span>
    </div>

    <div class="settings-content collapsible-content" :class="{ collapsed: isCollapsed }">
        <form @submit.prevent="saveSettings">
            <div class="settings-section">
                <h3>Network & API</h3>
                
                <div class="form-group">
                    <label>Listen Address</label>
                    <select v-model="formData.listenAddress" class="form-control">
                        <option v-for="ip in store.availableIps" :key="ip" :value="ip">{{ ip }}</option>
                    </select>
                </div>
                
                <div class="form-group">
                    <label>API Port</label>
                    <input type="number" v-model.number="formData.networkPort" min="1" max="65535" class="form-control">
                </div>
                
                <div class="form-group">
                    <label>Log Level</label>
                    <select v-model="formData.logLevel" class="form-control">
                        <option value="DEBUG">DEBUG (All logs, including serial traffic)</option>
                        <option value="INFO">INFO (Normal operation logs)</option>
                        <option value="WARN">WARN (Warnings and errors only)</option>
                        <option value="ERROR">ERROR (Errors only)</option>
                    </select>
                </div>
                
                <div class="form-group checkbox-group">
                    <label class="toggle-switch">
                        <input type="checkbox" v-model="formData.enableAlpacaDiscovery">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label">Enable Alpaca Discovery (UDP 32227)</span>
                </div>
                
                <div class="form-group checkbox-group">
                    <label class="toggle-switch">
                        <input type="checkbox" v-model="formData.enableNotifications">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label">Enable System Tray Notifications</span>
                </div>
            </div>

            <div class="settings-section">
                <h3>Hardware Setup</h3>

                <div class="form-group">
                    <label>Serial Port
                        <i class="ri-question-line info-hint" title="The serial port the panel is connected to. This list is a plain enumeration -- it never opens or resets a USB serial adapter, so refreshing is always safe. If the panel's port isn't shown, plug it in (or wait for Windows to finish installing its driver) and hit Refresh."></i>
                    </label>
                    <div style="display: flex; gap: 8px;">
                        <select v-model="formData.serialPortName" class="form-control" style="flex: 1;">
                            <option value="" disabled>Select a port...</option>
                            <option v-for="port in portOptions" :key="port.name" :value="port.name">
                                {{ port.name }}{{ port.isUsb ? ' (USB' + (port.vid ? ' ' + port.vid + ':' + port.pid : '') + ')' : '' }}{{ port.stale ? ' (currently unavailable)' : '' }}
                            </option>
                        </select>
                        <button type="button" class="btn-secondary" @click="refreshSerialPorts" :disabled="isRefreshingPorts" title="Re-scan available serial ports">
                            <i class="ri-refresh-line"></i> {{ isRefreshingPorts ? 'Refreshing...' : 'Refresh' }}
                        </button>
                    </div>
                </div>

                <div class="form-group checkbox-group">
                    <label class="toggle-switch">
                        <input type="checkbox" v-model="formData.serialConnectOnDemand">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label">Connect to Panel on Demand</span>
                    <i class="ri-question-line info-hint"
                       title="Off (default): keep trying to connect in the background at all times. On: stay disconnected until an Alpaca client connects or the dashboard's Connect button is used — useful if the panel is only plugged in occasionally, to avoid constant reconnect attempts (and their log entries) while it's absent."></i>
                </div>

                <div class="form-group" v-if="formData.serialConnectOnDemand">
                    <label>Idle Release Timeout (Seconds)
                        <i class="ri-question-line info-hint"
                           title="How long to keep the port open after an Alpaca client disconnects, in case of a quick reconnect (e.g. an equipment profile switch), before releasing it."></i>
                    </label>
                    <input type="number" v-model.number="formData.idleReleaseTimeoutSeconds" min="1" max="3600" class="form-control">
                </div>

                <div class="form-group">
                    <label>Panel Revision
                        <i class="ri-question-line info-hint" title="Which Gemini Flat Panel firmware to expect. &quot;Auto-detect&quot; tries every known revision in turn; pick a specific one only if auto-detection picks the wrong device or you want to skip straight to it."></i>
                    </label>
                    <select v-model="formData.panelRevision" class="form-control">
                        <option value="auto">Auto-detect</option>
                        <option value="rev2">Revision 2</option>
                        <option value="lite">Lite (no motorized cover)</option>
                        <option value="pro">Pro</option>
                    </select>
                </div>

                <div class="form-group checkbox-group">
                    <label class="toggle-switch">
                        <input type="checkbox" v-model="formData.enableBeep">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label">Enable Sound / Beep Feedback</span>
                    <span v-if="store.liveStatus.panel_revision !== '-' && !store.liveStatus.has_beep" style="font-size: 0.75rem; opacity: 0.6;">
                        (not supported by the connected {{ store.liveStatus.panel_revision }} panel)
                    </span>
                </div>
                
                <div class="form-group">
                    <label>Max Connection Retries
                        <i class="ri-question-line info-hint" title="Number of connection retry attempts before reporting disconnect to Alpaca clients."></i>
                    </label>
                    <input type="number" v-model.number="formData.maxConnectionRetries" min="0" max="20" class="form-control">
                </div>

                <div class="form-group">
                    <label>Connection Retry Interval (Seconds)
                        <i class="ri-question-line info-hint" title="Wait time between reconnection attempts when the serial connection is lost."></i>
                    </label>
                    <input type="number" v-model.number="formData.connectionRetryInterval" min="0.1" max="10" step="0.1" class="form-control">
                </div>

                <div class="form-group">
                    <label>Cover Movement Timeout (Seconds)
                        <i class="ri-question-line info-hint" title="Maximum time to wait for the cover to open or close before setting it to Unknown."></i>
                    </label>
                    <input type="number" v-model.number="formData.coverTimeout" min="5" max="300" class="form-control">
                </div>

            </div>

            <div class="settings-section">
                <h3>Dew Heater</h3>

                <div class="form-group">
                    <label>Backend
                        <i class="ri-question-line info-hint" title="What the automatic dew-control loop actually drives. &quot;None&quot; keeps the dashboard's Dew Heater card hidden entirely. &quot;Built-in&quot; uses the connected Pro panel's own heater output. &quot;External Alpaca Switch&quot; drives a channel on a separate switch device instead — the only option that works on a Rev2/Lite panel, which has no heater output of its own."></i>
                    </label>
                    <select v-model="formData.dewHeaterBackend" class="form-control">
                        <option value="none">None (feature disabled)</option>
                        <option value="built-in">Built-in Pro heater</option>
                        <option value="external">External Alpaca Switch</option>
                    </select>
                </div>
                <p class="help-text" v-if="formData.dewHeaterBackend !== 'none'">
                    Configure the automatic heating curve, and (for an external switch) the target
                    device and channel, from the Dew Heater card's own "Setup..." link on the dashboard.
                </p>
            </div>

            <div class="form-actions">
                <button type="button" class="btn-secondary" @click="initFormData" :disabled="!hasChanges || isSaving">Discard</button>
                <button type="submit" class="btn-primary" :disabled="!hasChanges || isSaving">
                    <i class="ri-save-line"></i> {{ isSaving ? 'Saving...' : 'Save Settings' }}
                </button>
            </div>
        </form>
    </div>
  </div>
</template>

<style scoped>
.collapsible-header {
    display: flex;
    justify-content: space-between;
    align-items: center;
    cursor: pointer;
    user-select: none;
    padding: 0.5rem 0;
    border-bottom: 1px solid rgba(255, 255, 255, 0.05);
    margin-bottom: 1rem;
}

.collapsible-header:hover .toggle-icon {
    border-color: #fff;
}

.collapsible-header h2 {
    margin: 0;
    font-size: 1.25rem;
    font-weight: 500;
    text-transform: uppercase;
    color: var(--accent-color);
    letter-spacing: 1px;
}

.settings-container {
    padding: 1.5rem;
    transition: padding 0.25s ease;
}

.settings-container.collapsed {
    padding-top: 0.75rem;
    padding-bottom: 0.75rem;
}

.settings-content {
    margin-top: 1rem;
}

.settings-section {
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 8px;
    padding: 1.5rem;
    margin-bottom: 1.5rem;
}

.settings-section h3 {
    margin-top: 0;
    margin-bottom: 1rem;
    color: var(--accent-color);
    border-bottom: 1px solid rgba(255, 255, 255, 0.1);
    padding-bottom: 0.5rem;
}

.form-group {
    margin-bottom: 1rem;
}

.form-group label {
    display: block;
    margin-bottom: 0.5rem;
    font-size: 0.95rem;
    color: var(--text-secondary);
}

.form-control {
    width: 100%;
    padding: 0.75rem;
    background: rgba(0, 0, 0, 0.2);
    border: 1px solid rgba(255, 255, 255, 0.2);
    border-radius: 6px;
    color: white;
    font-family: inherit;
    transition: border-color 0.3s ease;
}

.form-control:focus {
    outline: none;
    border-color: var(--accent-color);
}

.checkbox-group {
    display: flex;
    align-items: center;
    gap: 10px;
    margin-top: 0.5rem;
}

.checkbox-label {
    font-size: 0.95rem;
}
</style>
