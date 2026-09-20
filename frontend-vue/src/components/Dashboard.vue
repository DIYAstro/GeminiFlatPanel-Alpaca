<script setup>
import { computed, ref, watch } from 'vue'
import { useDeviceStore } from '../stores/device'
import { useModalStore } from '../stores/modal'
import { lockBodyScroll, unlockBodyScroll } from '../utils/modalScrollLock'

const store = useDeviceStore()
const modal = useModalStore()

// The 254-cap rule for panels without low/high-bank switching (Pro) is decided in
// exactly one place, internal/serial.EffectiveMaxBrightness — GetLiveStatus() already
// applies it and reports the result as effective_max_brightness, so this just reflects
// that instead of re-deriving the same rule a third time.
const maxBrightness = computed(() => {
    return store.liveStatus.effective_max_brightness || store.proxyConfig?.maxBrightness || 510
})

const brightnessVal = ref(0)
const dismissWarning = ref(false)

// Heater percent (Pro only) — not yet physically confirmed to actually drive a
// connected heater element, see protocol_pro.go's SupportsHeater doc comment.
const heaterVal = ref(0)

// Light Setup Modal state
const showLightSetupModal = ref(false)
const isSavingLightSetup = ref(false)

// Calibration saving states
const isSavingClosed = ref(false)
const isSavingOpened = ref(false)
const closedSavedSuccess = ref(false)
const openedSavedSuccess = ref(false)

// Auto-calibration (Pro only, experimental — see protocol_pro.go's
// SupportsAutoCalibrate doc comment). Each takes ~22s+ on real hardware.
const isAutoCalibratingClosed = ref(false)
const isAutoCalibratingOpen = ref(false)
const autoClosedSuccess = ref(false)
const autoOpenedSuccess = ref(false)
const lightSetupData = ref({
    maxBrightness: 510,
    highBankStartValue: 9,
    blockLightWhenOpen: false,
    settleTime: 2.0
})

// Dew Control Setup Modal state
const showDewControlModal = ref(false)
const isSavingDewControl = ref(false)
const isTestingObservingConditions = ref(false)
const observingConditionsTestResult = ref(null) // { ok, temperature, dewPoint, error }
const isDiscoveringObservingConditions = ref(false)
const discoveredObservingConditions = ref(null) // array of {baseUrl, deviceNumber, deviceName}, null before first scan
const discoveryError = ref('')
// External dew-heater switch discovery/channel-picker state -- only relevant when
// proxyConfig.dewHeaterBackend is "external" (see Settings.vue for the backend
// selector itself; this modal only configures the details once it's chosen).
const isDiscoveringSwitches = ref(false)
const discoveredSwitches = ref(null) // array of {baseUrl, deviceNumber, deviceName}, null before first scan
const switchDiscoveryError = ref('')
const isListingChannels = ref(false)
const switchChannels = ref(null) // array of {id, name, minValue, maxValue, step, isRheostat}, null before first list
const switchListError = ref('')
// Data Source 1/2 are collapsible (reused .collapsible-header/.collapsible-content
// mechanism, same as Settings.vue's "Configuration" panel). Persisted in
// localStorage the same way Settings.vue persists its own collapsed state, and
// default to collapsed (unlike Settings.vue, which defaults to expanded) -- loaded
// fresh each time the modal opens, see openDewControlSetup below.
const obsSectionExpanded = ref(false)
const meteoSectionExpanded = ref(false)
const DEW_CONTROL_OBS_COLLAPSE_KEY = 'dew-control-obs-section-expanded'
const DEW_CONTROL_METEO_COLLAPSE_KEY = 'dew-control-meteo-section-expanded'

function loadDataSourceCollapseState() {
    try {
        obsSectionExpanded.value = localStorage.getItem(DEW_CONTROL_OBS_COLLAPSE_KEY) === 'true'
        meteoSectionExpanded.value = localStorage.getItem(DEW_CONTROL_METEO_COLLAPSE_KEY) === 'true'
    } catch (e) {
        // localStorage can throw (private browsing, disabled site data, etc.) -- fall
        // back to the collapsed default, same as a first-ever visit.
        obsSectionExpanded.value = false
        meteoSectionExpanded.value = false
    }
}

function toggleObsSection() {
    obsSectionExpanded.value = !obsSectionExpanded.value
    try {
        localStorage.setItem(DEW_CONTROL_OBS_COLLAPSE_KEY, obsSectionExpanded.value)
    } catch (e) { /* not persisted this session, toggle still works */ }
}

function toggleMeteoSection() {
    meteoSectionExpanded.value = !meteoSectionExpanded.value
    try {
        localStorage.setItem(DEW_CONTROL_METEO_COLLAPSE_KEY, meteoSectionExpanded.value)
    } catch (e) { /* not persisted this session, toggle still works */ }
}
const dewControlData = ref({
    enableAutoDewControl: false,
    dewControlIntervalMinutes: 5,
    dewControlDeltaFullPower: 1.0,
    dewControlDeltaZeroPower: 5.0,
    dewControlOnlyWhenOpen: false,
    dewControlFailsafeEnabled: false,
    dewControlFailsafePercent: 0,
    observingConditionsUrl: '',
    observingConditionsDeviceNumber: 0,
    enableOpenMeteo: false,
    weatherLatitude: 0,
    weatherLongitude: 0,
    dewHeaterSwitchUrl: '',
    dewHeaterSwitchDeviceNumber: 0,
    dewHeaterSwitchId: 0,
    dewHeaterSwitchIsRheostat: true,
    dewControlOnOffTargetDelta: 3.0,
    dewControlOnOffHysteresis: 1.0
})

// Whether a channel has actually been picked from the discovered list this session --
// distinct from dewHeaterSwitchIsRheostat's *value* (which defaults to true and is
// meaningless until a channel is chosen), so the curve-vs-threshold fields below stay
// hidden until there's a real selection to reflect.
const hasSelectedSwitchChannel = ref(false)
// Display-only (not persisted) -- the config only stores dewHeaterSwitchId, but the
// name reads far better in the "Selected: ..." confirmation than a bare number.
// Empty whenever the channel list hasn't been (re-)fetched this session, e.g. right
// after reopening the modal on a previously-saved config -- falls back to showing
// just the Id in that case, until "List Channels" is run again.
const selectedSwitchChannelName = ref('')

// Locks page scroll while any of this component's 3 modals is open (see
// utils/modalScrollLock.js) -- only one of these is ever actually reachable at a
// time in practice (each is a full-screen overlay blocking interaction with
// whatever would open another), but combining them into one watched boolean is
// simpler than 3 separate watchers making the same lock/unlock calls.
const isAnyModalOpen = computed(() =>
    store.showCalibrationModal || showLightSetupModal.value || showDewControlModal.value
)
watch(isAnyModalOpen, (open) => {
    if (open) {
        lockBodyScroll()
    } else {
        unlockBodyScroll()
    }
})

watch(() => store.liveStatus.brightness, (newVal) => {
    const slider = document.querySelector('.brightness-slider')
    if (!slider || document.activeElement !== slider) {
        brightnessVal.value = newVal
    }
}, { immediate: true })

// Tracks the external backend's own percent once it's active, instead of the
// built-in heater's -- both are surfaced as heaterVal to the same slider, since only
// one of the two is ever relevant at a time (see the slider's own v-if).
watch(() => store.liveStatus.dew_control_backend === 'external'
    ? store.liveStatus.dew_control_external_percent
    : store.liveStatus.heater_percent, (newVal) => {
    const slider = document.querySelector('.heater-slider')
    if (!slider || document.activeElement !== slider) {
        heaterVal.value = newVal
    }
}, { immediate: true })

// An external switch is reached over HTTP, independent of the panel's own serial
// connection -- store.isConnected (which reflects that serial link) would otherwise
// wrongly disable manual control whenever there's no panel connected at all, exactly
// the Rev2/Lite-without-a-panel-heater case the external backend exists for.
const isHeaterControllable = computed(() =>
    store.liveStatus.dew_control_backend === 'external' ? true : store.isConnected
)

const coverStateText = computed(() => {
    switch (store.liveStatus.cover_state) {
        case 0: return 'Not Present'
        case 1: return 'Closed'
        case 2: return 'Moving'
        case 3: return 'Open'
        default: return 'Unknown/Moving'
    }
})

const coverStateClass = computed(() => {
    switch (store.liveStatus.cover_state) {
        case 0: return 'status-not-present'
        case 1: return 'status-closed'
        case 2: return 'status-moving'
        case 3: return 'status-open'
        default: return 'status-unknown'
    }
})

// Call setBrightness in the store. Since we have a settle time,
// we fetch live status after the sleep duration completes.
// We'll dynamic-wait a bit longer than the config settleTime.
function updateBrightness() {
    store.setBrightness(parseInt(brightnessVal.value))
}

function triggerOpen() { store.openCover() }
function triggerClose() { store.closeCover() }
function triggerHalt() { store.haltCover() }

function jog(angle) { store.jogMotor(angle) }

function updateHeater() {
    store.setHeaterPower(parseInt(heaterVal.value))
}

async function setClosed() {
    isSavingClosed.value = true
    closedSavedSuccess.value = false
    try {
        await store.saveClosedPosition()
        closedSavedSuccess.value = true
        setTimeout(() => {
            closedSavedSuccess.value = false
        }, 2000)
    } catch (e) {
        modal.error('Failed to save closed position: ' + e.message)
    } finally {
        isSavingClosed.value = false
    }
}

async function setOpened() {
    isSavingOpened.value = true
    openedSavedSuccess.value = false
    try {
        await store.saveOpenedPosition()
        openedSavedSuccess.value = true
        setTimeout(() => {
            openedSavedSuccess.value = false
        }, 2000)
    } catch (e) {
        modal.error('Failed to save opened position: ' + e.message)
    } finally {
        isSavingOpened.value = false
    }
}

async function autoCalibrateClosed() {
    isAutoCalibratingClosed.value = true
    autoClosedSuccess.value = false
    try {
        await store.autoCalibrateClosed()
        autoClosedSuccess.value = true
        setTimeout(() => {
            autoClosedSuccess.value = false
        }, 2000)
    } catch (e) {
        modal.error('Failed to auto-calibrate closed position: ' + e.message)
    } finally {
        isAutoCalibratingClosed.value = false
    }
}

async function autoCalibrateOpen() {
    isAutoCalibratingOpen.value = true
    autoOpenedSuccess.value = false
    try {
        await store.autoCalibrateOpen()
        autoOpenedSuccess.value = true
        setTimeout(() => {
            autoOpenedSuccess.value = false
        }, 2000)
    } catch (e) {
        modal.error('Failed to auto-calibrate open position: ' + e.message)
    } finally {
        isAutoCalibratingOpen.value = false
    }
}

function openLightSetup() {
    if (store.proxyConfig) {
        lightSetupData.value = {
            maxBrightness: store.proxyConfig.maxBrightness || 510,
            highBankStartValue: store.proxyConfig.highBankStartValue !== undefined ? store.proxyConfig.highBankStartValue : 9,
            blockLightWhenOpen: store.proxyConfig.blockLightWhenOpen || false,
            settleTime: (store.proxyConfig.settleTime !== undefined ? store.proxyConfig.settleTime : 2000) / 1000
        }
    }
    showLightSetupModal.value = true
}

async function saveLightSetup() {
    isSavingLightSetup.value = true
    try {
        const updatedConfig = {
            ...store.proxyConfig,
            maxBrightness: parseInt(lightSetupData.value.maxBrightness),
            highBankStartValue: parseInt(lightSetupData.value.highBankStartValue),
            blockLightWhenOpen: !!lightSetupData.value.blockLightWhenOpen,
            settleTime: Math.max(0, Math.round(parseFloat(lightSetupData.value.settleTime) * 1000))
        }
        await store.saveProxyConfig(updatedConfig)
        showLightSetupModal.value = false
        modal.success('Flat Panel setup saved successfully.')
    } catch (e) {
        modal.error('Failed to save setup: ' + e.message)
    } finally {
        isSavingLightSetup.value = false
    }
}

function openDewControlSetup() {
    if (store.proxyConfig) {
        dewControlData.value = {
            enableAutoDewControl: store.proxyConfig.enableAutoDewControl || false,
            dewControlIntervalMinutes: store.proxyConfig.dewControlIntervalMinutes || 5,
            dewControlDeltaFullPower: store.proxyConfig.dewControlDeltaFullPower !== undefined ? store.proxyConfig.dewControlDeltaFullPower : 1.0,
            dewControlDeltaZeroPower: store.proxyConfig.dewControlDeltaZeroPower !== undefined ? store.proxyConfig.dewControlDeltaZeroPower : 5.0,
            dewControlOnlyWhenOpen: store.proxyConfig.dewControlOnlyWhenOpen || false,
            dewControlFailsafeEnabled: store.proxyConfig.dewControlFailsafeEnabled || false,
            dewControlFailsafePercent: store.proxyConfig.dewControlFailsafePercent || 0,
            observingConditionsUrl: store.proxyConfig.observingConditionsUrl || '',
            observingConditionsDeviceNumber: store.proxyConfig.observingConditionsDeviceNumber || 0,
            enableOpenMeteo: store.proxyConfig.enableOpenMeteo || false,
            weatherLatitude: store.proxyConfig.weatherLatitude || 0,
            weatherLongitude: store.proxyConfig.weatherLongitude || 0,
            dewHeaterSwitchUrl: store.proxyConfig.dewHeaterSwitchUrl || '',
            dewHeaterSwitchDeviceNumber: store.proxyConfig.dewHeaterSwitchDeviceNumber || 0,
            dewHeaterSwitchId: store.proxyConfig.dewHeaterSwitchId || 0,
            dewHeaterSwitchIsRheostat: store.proxyConfig.dewHeaterSwitchIsRheostat !== undefined ? store.proxyConfig.dewHeaterSwitchIsRheostat : true,
            dewControlOnOffTargetDelta: store.proxyConfig.dewControlOnOffTargetDelta !== undefined ? store.proxyConfig.dewControlOnOffTargetDelta : 3.0,
            dewControlOnOffHysteresis: store.proxyConfig.dewControlOnOffHysteresis !== undefined ? store.proxyConfig.dewControlOnOffHysteresis : 1.0
        }
        // A previously-saved external switch URL already implies a channel was picked
        // in an earlier session -- show the curve/threshold fields right away instead
        // of hiding them until the user re-runs discovery for no reason.
        hasSelectedSwitchChannel.value = !!store.proxyConfig.dewHeaterSwitchUrl
        selectedSwitchChannelName.value = ''
    }
    observingConditionsTestResult.value = null
    discoveredObservingConditions.value = null
    discoveryError.value = ''
    discoveredSwitches.value = null
    switchDiscoveryError.value = ''
    switchChannels.value = null
    switchListError.value = ''
    loadDataSourceCollapseState()
    showDewControlModal.value = true
}

async function saveDewControlSetup() {
    isSavingDewControl.value = true
    try {
        const updatedConfig = {
            ...store.proxyConfig,
            enableAutoDewControl: !!dewControlData.value.enableAutoDewControl,
            dewControlIntervalMinutes: parseInt(dewControlData.value.dewControlIntervalMinutes),
            dewControlDeltaFullPower: parseFloat(dewControlData.value.dewControlDeltaFullPower),
            dewControlDeltaZeroPower: parseFloat(dewControlData.value.dewControlDeltaZeroPower),
            dewControlOnlyWhenOpen: !!dewControlData.value.dewControlOnlyWhenOpen,
            dewControlFailsafeEnabled: !!dewControlData.value.dewControlFailsafeEnabled,
            dewControlFailsafePercent: parseInt(dewControlData.value.dewControlFailsafePercent) || 0,
            observingConditionsUrl: dewControlData.value.observingConditionsUrl.trim(),
            observingConditionsDeviceNumber: parseInt(dewControlData.value.observingConditionsDeviceNumber) || 0,
            enableOpenMeteo: !!dewControlData.value.enableOpenMeteo,
            weatherLatitude: parseFloat(dewControlData.value.weatherLatitude) || 0,
            weatherLongitude: parseFloat(dewControlData.value.weatherLongitude) || 0,
            dewHeaterSwitchUrl: dewControlData.value.dewHeaterSwitchUrl.trim(),
            dewHeaterSwitchDeviceNumber: parseInt(dewControlData.value.dewHeaterSwitchDeviceNumber) || 0,
            dewHeaterSwitchId: parseInt(dewControlData.value.dewHeaterSwitchId) || 0,
            dewHeaterSwitchIsRheostat: !!dewControlData.value.dewHeaterSwitchIsRheostat,
            dewControlOnOffTargetDelta: parseFloat(dewControlData.value.dewControlOnOffTargetDelta) || 0,
            dewControlOnOffHysteresis: Math.max(0, parseFloat(dewControlData.value.dewControlOnOffHysteresis) || 0)
        }
        await store.saveProxyConfig(updatedConfig)
        showDewControlModal.value = false
        modal.success('Dew control setup saved successfully.')
    } catch (e) {
        modal.error('Failed to save setup: ' + e.message)
    } finally {
        isSavingDewControl.value = false
    }
}

async function testObservingConditionsConnection() {
    isTestingObservingConditions.value = true
    observingConditionsTestResult.value = null
    try {
        const result = await store.testObservingConditions(
            dewControlData.value.observingConditionsUrl.trim(),
            parseInt(dewControlData.value.observingConditionsDeviceNumber) || 0
        )
        observingConditionsTestResult.value = result
    } catch (e) {
        observingConditionsTestResult.value = { ok: false, error: e.message }
    } finally {
        isTestingObservingConditions.value = false
    }
}

async function discoverObservingConditions() {
    isDiscoveringObservingConditions.value = true
    discoveryError.value = ''
    discoveredObservingConditions.value = null
    try {
        discoveredObservingConditions.value = await store.discoverObservingConditions()
    } catch (e) {
        discoveryError.value = e.message
    } finally {
        isDiscoveringObservingConditions.value = false
    }
}

function selectDiscoveredDevice(device) {
    dewControlData.value.observingConditionsUrl = device.baseUrl
    dewControlData.value.observingConditionsDeviceNumber = device.deviceNumber
    observingConditionsTestResult.value = null
}

async function discoverDewHeaterSwitches() {
    isDiscoveringSwitches.value = true
    switchDiscoveryError.value = ''
    discoveredSwitches.value = null
    try {
        discoveredSwitches.value = await store.discoverDewHeaterSwitches()
    } catch (e) {
        switchDiscoveryError.value = e.message
    } finally {
        isDiscoveringSwitches.value = false
    }
}

function selectDiscoveredSwitch(device) {
    dewControlData.value.dewHeaterSwitchUrl = device.baseUrl
    dewControlData.value.dewHeaterSwitchDeviceNumber = device.deviceNumber
    switchChannels.value = null
    switchListError.value = ''
    hasSelectedSwitchChannel.value = false
    selectedSwitchChannelName.value = ''
}

// Also serves as this device's "test connection" step -- see store.listSwitchChannels.
async function listChannelsForSelectedSwitch() {
    isListingChannels.value = true
    switchListError.value = ''
    switchChannels.value = null
    try {
        const result = await store.listSwitchChannels(
            dewControlData.value.dewHeaterSwitchUrl.trim(),
            parseInt(dewControlData.value.dewHeaterSwitchDeviceNumber) || 0
        )
        if (result.ok) {
            switchChannels.value = result.channels || []
        } else {
            switchListError.value = result.error
        }
    } catch (e) {
        switchListError.value = e.message
    } finally {
        isListingChannels.value = false
    }
}

function selectSwitchChannel(channel) {
    dewControlData.value.dewHeaterSwitchId = channel.id
    dewControlData.value.dewHeaterSwitchIsRheostat = channel.isRheostat
    hasSelectedSwitchChannel.value = true
    selectedSwitchChannelName.value = channel.name
}

function detectLocation() {
    if (!navigator.geolocation) {
        modal.error('Geolocation is not available in this browser.')
        return
    }
    navigator.geolocation.getCurrentPosition(
        (pos) => {
            dewControlData.value.weatherLatitude = pos.coords.latitude
            dewControlData.value.weatherLongitude = pos.coords.longitude
        },
        (err) => modal.error('Failed to detect location: ' + err.message)
    )
}
</script>

<template>
  <div class="panel glass-panel dashboard-container">
    <!-- Calibration Warning -->
    <div v-if="store.isConnected && store.liveStatus.device_ready === false && !dismissWarning" class="calibration-warning">
      <div class="calibration-warning-message">
        <i class="ri-error-warning-line"></i>
        <div>
          <strong>Calibration not set!</strong>
          <span>The device reports that the motor positions have not been saved yet. You must first calibrate the closed position, then the open position.</span>
        </div>
      </div>
      <div class="calibration-warning-actions">
        <button class="btn-primary calibration-warning-btn" @click="store.showCalibrationModal = true">
          <i class="ri-tools-line"></i> Calibrate Motor
        </button>
        <button class="calibration-warning-dismiss" @click="dismissWarning = true" title="Dismiss warning">
          <i class="ri-close-line"></i>
        </button>
      </div>
    </div>
    
    <div class="status-grid" :class="{ 'has-heater': store.liveStatus.has_heater }">
      <!-- Light Control -->
      <div class="control-card">
        <div class="card-header-row">
            <h3><i class="ri-lightbulb-line"></i> Flat Panel Light</h3>
            <button @click="openLightSetup" class="calibrate-link" :disabled="!store.isConnected" title="Open light setup settings">
                <i class="ri-settings-4-line"></i> Setup...
            </button>
        </div>
        <div class="card-stats">
            <div class="card-stat">
                <span class="card-stat-label">Status</span>
                <span class="card-stat-value" :class="store.liveStatus.light_state === 1 ? 'status-on' : 'status-off'">
                    {{ store.liveStatus.light_state === 1 ? 'ON' : 'OFF' }}
                </span>
            </div>
            <div class="card-stat">
                <span class="card-stat-label">Mode</span>
                <span class="card-stat-value">{{ store.liveStatus.high_mode ? 'High Bank (Y1)' : 'Low Bank (Y0)' }}</span>
            </div>
            <div class="card-stat card-stat-wide">
                <span class="card-stat-label">Current Brightness</span>
                <span class="card-stat-value">{{ store.liveStatus.brightness }} / {{ maxBrightness }}</span>
            </div>
        </div>

        <div class="brightness-slider-container" style="margin-top: 1rem;">
            <input type="range" 
                   min="0" :max="maxBrightness" 
                   v-model.number="brightnessVal" 
                   :disabled="!store.isConnected"
                   class="brightness-slider">
            <input type="number" 
                   min="0" :max="maxBrightness" 
                   v-model.number="brightnessVal" 
                   :disabled="!store.isConnected"
                   class="brightness-slider-input">
        </div>
        <button class="btn-primary" style="margin-top: 1rem; width: 100%" @click="updateBrightness" :disabled="!store.isConnected">Set Brightness</button>
      </div>

      <!-- Cover Control (only panels with a motorized cover -- same v-if pattern
           the Heater card already uses, rather than always rendering this card and
           swapping its content for a "no motorized cover" message) -->
      <div v-if="store.liveStatus.has_cover" class="control-card">
        <div class="card-header-row">
            <h3><i class="ri-drag-move-fill"></i> Cover Control</h3>
            <button @click="store.showCalibrationModal = true" class="calibrate-link" :disabled="!store.isConnected" title="Open calibration settings">
                <i class="ri-settings-4-line"></i> Calibrate...
            </button>
        </div>
        <div class="card-stats">
            <div class="card-stat">
                <span class="card-stat-label">State</span>
                <span class="card-stat-value" :class="coverStateClass">{{ coverStateText }}</span>
            </div>
        </div>

        <!-- .action-buttons is pinned to the bottom of the (grid-stretched) card via
             margin-top: auto, so these buttons line up with Dew Heater's "Set Heater
             Power" button next to it instead of leaving a bare gap under them. -->
        <div class="action-buttons">
            <button class="btn-primary" @click="triggerOpen" :disabled="!store.isConnected">Open</button>
            <button class="btn-danger" @click="triggerClose" :disabled="!store.isConnected">Close</button>
            <!-- Pro only (has_halt) -- other revisions' firmware has no way to
                 interrupt an in-progress move at all, see serial.HaltCover. Only
                 enabled while actually moving; harmless no-op otherwise either way. -->
            <button v-if="store.liveStatus.has_halt" class="btn-warning" @click="triggerHalt"
                    :disabled="!store.isConnected || store.liveStatus.cover_state !== 2"
                    title="Stop the cover wherever it currently is">Stop</button>
        </div>
      </div>

      <!-- Heater Control: the Pro panel's own built-in heater, or an external Alpaca
           Switch channel (see Settings.vue's "Dew Heater" backend selector). Gated
           purely on the backend choice, not has_heater -- a Pro panel always reports
           has_heater=true regardless of whether dew control is actually wanted, which
           is exactly the "card always shows even when unused" problem the backend
           setting exists to avoid. -->
      <div v-if="store.liveStatus.dew_control_backend === 'built-in' || store.liveStatus.dew_control_backend === 'external'" class="control-card">
        <div class="card-header-row">
            <h3><i class="ri-fire-line"></i> Dew Heater</h3>
            <button @click="openDewControlSetup" class="calibrate-link" :disabled="!store.isConnected" title="Open automatic dew control setup">
                <i class="ri-settings-4-line"></i> Setup...
            </button>
        </div>
        <div class="card-stats">
            <div class="card-stat" v-if="store.liveStatus.dew_control_backend === 'external' && store.liveStatus.dew_control_external_on !== undefined">
                <span class="card-stat-label">Status</span>
                <span class="card-stat-value" :class="store.liveStatus.dew_control_external_on ? 'status-on' : 'status-off'">
                    {{ store.liveStatus.dew_control_external_on ? 'ON' : 'OFF' }}
                </span>
            </div>
            <div class="card-stat" v-else>
                <span class="card-stat-label">Power</span>
                <span class="card-stat-value">{{ store.liveStatus.dew_control_backend === 'external' ? store.liveStatus.dew_control_external_percent : store.liveStatus.heater_percent }}%</span>
            </div>
            <div v-if="store.liveStatus.auto_dew_control_enabled" class="card-stat">
                <span class="card-stat-label">Mode</span>
                <span class="card-stat-value status-on"
                      :title="'A manual power change applies immediately but is only temporary — the next automatic tick will recompute and override it.' + (store.liveStatus.dew_control_last_source ? ' Data source: ' + store.liveStatus.dew_control_last_source + '.' : '')">Automatic</span>
            </div>
            <template v-if="store.liveStatus.auto_dew_control_enabled && store.liveStatus.dew_control_temperature !== undefined">
                <div class="card-stat">
                    <span class="card-stat-label">Ambient Temp</span>
                    <span class="card-stat-value">{{ store.liveStatus.dew_control_temperature.toFixed(1) }}°C</span>
                </div>
                <div class="card-stat">
                    <span class="card-stat-label">Dew Point</span>
                    <span class="card-stat-value">{{ store.liveStatus.dew_control_dew_point.toFixed(1) }}°C</span>
                </div>
            </template>
        </div>

        <template v-if="store.liveStatus.dew_control_backend !== 'external' || store.liveStatus.dew_control_external_percent !== undefined">
            <!-- margin-bottom (not just margin-top) is required here specifically: the
                 button below is pinned to the card's bottom edge via margin-top: auto
                 (see .control-card's comment), which shrinks toward 0 once this card's
                 own content is tall enough to fill the grid row on its own (e.g. all 4
                 stats + this slider) -- without a fixed gap here too, the slider and
                 button end up flush against each other with no breathing room. -->
            <div class="brightness-slider-container" style="margin-top: 1rem; margin-bottom: 1rem;">
                <input type="range"
                       min="0" max="100" step="10"
                       v-model.number="heaterVal"
                       :disabled="!isHeaterControllable"
                       class="brightness-slider heater-slider">
                <input type="number"
                       min="0" max="100" step="10"
                       v-model.number="heaterVal"
                       :disabled="!isHeaterControllable"
                       class="brightness-slider-input">
            </div>
            <!-- Pinned to the card's bottom edge via margin-top: auto, same mechanism as
                 Cover Control's action-buttons, so both line up. The "manual changes here
                 are temporary under auto mode" caveat that used to sit in a paragraph
                 below this button now lives in the Mode stat's tooltip above instead --
                 this button is deliberately the card's last element. -->
            <button class="btn-primary" style="margin-top: auto; width: 100%" @click="updateHeater" :disabled="!isHeaterControllable">Set Heater Power</button>
        </template>
        <p v-else class="help-text" style="margin-top: auto; margin-bottom: 0;">
            Manual control isn't available for an on/off switch — adjust the automatic threshold via Setup instead.
        </p>
      </div>
    </div>
  </div>

  <!-- Calibration Modal -->
  <Teleport to="body">
    <div v-if="store.showCalibrationModal" class="modal-overlay">
      <div class="modal-content setup-modal">
        <div class="modal-header">
            <h3 class="modal-title">
                <i class="ri-tools-line"></i> Motor Calibration
            </h3>
            <button @click="store.showCalibrationModal = false" class="close-btn" title="Close">
                &times;
            </button>
        </div>
        <div class="modal-body">
            <p class="help-text" style="font-size: 0.85rem; opacity: 0.8; margin-bottom: 1rem; line-height: 1.4;">
                Use these controls to jog the motor in small steps. First, adjust the cover to the physical closed position and click <strong>Save as Closed</strong>. Next, adjust it to the open position and click <strong>Save as Opened</strong>.
            </p>

            <!-- Chevron labels, not degrees: jog(n)'s n is in the panel's own raw
                 position units (see JogMotor's doc comment, internal/serial/serial.go),
                 not physical degrees -- a real, firmware-specific ratio between the two
                 exists for Pro (~5.4 units/degree) but isn't confirmed for Rev2, so
                 showing a degree figure here would misrepresent actual motion either
                 way. More chevrons = bigger step, same n values as before. -->
            <div style="background: rgba(0, 0, 0, 0.25); border: 1px solid rgba(255, 255, 255, 0.05); border-radius: 8px; padding: 0.75rem; margin-bottom: 1rem;">
              <label style="display: block; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.5px; color: var(--text-secondary); margin-bottom: 0.5rem; text-align: center;">
                Jog Position
              </label>
              <div class="jog-controls">
                  <button class="btn-secondary" @click="jog(-180)" :disabled="!store.isConnected">&lt;&lt;&lt;&lt;</button>
                  <button class="btn-secondary" @click="jog(-10)" :disabled="!store.isConnected">&lt;&lt;&lt;</button>
                  <button class="btn-secondary" @click="jog(-5)" :disabled="!store.isConnected">&lt;&lt;</button>
                  <button class="btn-secondary" @click="jog(-1)" :disabled="!store.isConnected">&lt;</button>
                  <button class="btn-secondary" @click="jog(1)" :disabled="!store.isConnected">&gt;</button>
                  <button class="btn-secondary" @click="jog(5)" :disabled="!store.isConnected">&gt;&gt;</button>
                  <button class="btn-secondary" @click="jog(10)" :disabled="!store.isConnected">&gt;&gt;&gt;</button>
                  <button class="btn-secondary" @click="jog(180)" :disabled="!store.isConnected">&gt;&gt;&gt;&gt;</button>
              </div>
            </div>
            
            <div class="save-controls" style="display: flex; gap: 10px; margin-top: 1rem;">
                <button 
                    class="btn-warning" 
                    @click="setClosed" 
                    :disabled="!store.isConnected || isSavingClosed" 
                    style="flex: 1; padding: 0.7rem;"
                >
                    <span v-if="isSavingClosed">Saving...</span>
                    <span v-else-if="closedSavedSuccess">✓ Saved!</span>
                    <span v-else>Save as Closed</span>
                </button>
                <button
                    class="btn-success"
                    @click="setOpened"
                    :disabled="!store.isConnected || isSavingOpened"
                    style="flex: 1; padding: 0.7rem;"
                >
                    <span v-if="isSavingOpened">Saving...</span>
                    <span v-else-if="openedSavedSuccess">✓ Saved!</span>
                    <span v-else>Save as Opened</span>
                </button>
            </div>

            <div v-if="store.liveStatus.has_auto_calibrate" style="margin-top: 1.25rem; padding-top: 1rem; border-top: 1px solid rgba(255,255,255,0.08);">
                <label style="display: block; font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.5px; color: var(--text-secondary); margin-bottom: 0.5rem;">
                    Auto-Calibrate
                </label>
                <p class="help-text" style="font-size: 0.8rem; margin-top: 0; margin-bottom: 0.75rem;">
                    Drives the motor to the physical hard stop and measures it automatically —
                    no manual jogging needed. Takes ~20-25 seconds per direction.
                </p>
                <div class="save-controls" style="display: flex; gap: 10px;">
                    <button
                        class="btn-warning"
                        @click="autoCalibrateClosed"
                        :disabled="!store.isConnected || isAutoCalibratingClosed || isAutoCalibratingOpen"
                        style="flex: 1; padding: 0.7rem;"
                    >
                        <span v-if="isAutoCalibratingClosed">Calibrating... (~20s)</span>
                        <span v-else-if="autoClosedSuccess">✓ Learned!</span>
                        <span v-else>Auto-Learn Closed</span>
                    </button>
                    <button
                        class="btn-success"
                        @click="autoCalibrateOpen"
                        :disabled="!store.isConnected || isAutoCalibratingClosed || isAutoCalibratingOpen"
                        style="flex: 1; padding: 0.7rem;"
                    >
                        <span v-if="isAutoCalibratingOpen">Calibrating... (~20s)</span>
                        <span v-else-if="autoOpenedSuccess">✓ Learned!</span>
                        <span v-else>Auto-Learn Open</span>
                    </button>
                </div>
            </div>

            <!-- Pro only (has_halt). Stops whatever is currently moving the motor --
                 jog, an Open/Close triggered elsewhere, or (the case this modal's own
                 controls can trigger) an auto-calibrate sweep -- see serial.HaltCover.
                 Enabled/disabled the same way as Cover Control's Stop button, keyed
                 off the same live cover_state rather than a modal-local flag, so it's
                 accurate regardless of what actually started the move.

                 Confirmed live: stopping a plain Open/Close leaves closedAngle/openAngle
                 untouched. Stopping an Auto-Calibrate sweep is different -- the firmware
                 can't tell a forced stop from genuinely reaching the hard stop, so it
                 immediately saves wherever the motor was as the new calibration value.
                 Not a bug to work around; it doubles as a way to deliberately set a
                 boundary short of the full physical travel. But it's the opposite of a
                 safe "cancel, nothing changes" -- the warning text below the button
                 makes that explicit rather than implying Stop is always non-destructive. -->
            <button v-if="store.liveStatus.has_halt" class="btn-warning" @click="triggerHalt"
                    :disabled="!store.isConnected || store.liveStatus.cover_state !== 2"
                    style="width: 100%; margin-top: 1rem;"
                    title="Stops the motor immediately. During Open/Close this is a safe interrupt. During Auto-Calibrate it instead saves the current position as the new calibration value right away.">Stop</button>
            <p v-if="store.liveStatus.has_halt" class="help-text" style="font-size: 0.75rem; opacity: 0.7; margin-top: 0.4rem; margin-bottom: 0;">
                During Auto-Calibrate, Stop doesn't cancel — it saves the current position as
                the new calibration value immediately (useful to deliberately cap travel short
                of the physical hard stop). During a plain Open/Close it's a safe interrupt.
            </p>
        </div>
      </div>
    </div>
  </Teleport>

  <!-- Flat Panel Setup Modal -->
  <Teleport to="body">
    <div v-if="showLightSetupModal" class="modal-overlay">
      <div class="modal-content setup-modal">
        <div class="modal-header">
            <h3 class="modal-title">
                <i class="ri-settings-4-line"></i> Flat Panel Setup
            </h3>
            <button @click="showLightSetupModal = false" class="close-btn" title="Close">
                &times;
            </button>
        </div>
        <div class="modal-body">
            <form @submit.prevent="saveLightSetup">
                <template v-if="store.liveStatus.has_brightness_mode">
                    <div class="form-group">
                        <label>Max Brightness Level (Typically 510)</label>
                        <input type="number" v-model.number="lightSetupData.maxBrightness" min="255" max="10000" class="form-control">
                    </div>

                    <div class="form-group">
                        <label>High Bank Start Value (0-254)</label>
                        <span style="display: block; font-size: 0.75rem; opacity: 0.6; margin-top: -0.25rem; margin-bottom: 0.4rem;">Calibrates smooth transition at brightness 255</span>
                        <input type="number" v-model.number="lightSetupData.highBankStartValue" min="0" max="254" class="form-control">
                    </div>
                </template>
                <p v-else class="help-text" style="margin-top: 0;">
                    This panel has no low/high-bank switching, so brightness tops out at 254 — the max brightness and high-bank fields don't apply here.
                </p>

                <div class="form-group">
                    <label>Settle Time (Seconds)</label>
                    <span style="display: block; font-size: 0.75rem; opacity: 0.6; margin-top: -0.25rem; margin-bottom: 0.4rem;">Delay before returning success response after setting brightness</span>
                    <input type="number" v-model.number="lightSetupData.settleTime" min="0" max="60" step="0.1" class="form-control">
                </div>
                
                <div v-if="store.liveStatus.has_cover" class="form-group checkbox-group" style="margin-top: 0.5rem; margin-bottom: 1.2rem;">
                    <label class="toggle-switch">
                        <input type="checkbox" v-model="lightSetupData.blockLightWhenOpen">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label" style="font-size: 0.95rem;">Block flat panel light when cover is open</span>
                </div>
                
                <div class="form-actions" style="display: flex; gap: 10px; justify-content: flex-end; margin-top: 1.25rem;">
                    <button type="button" class="btn-secondary" @click="showLightSetupModal = false" :disabled="isSavingLightSetup" style="padding: 0.5rem 1rem;">Cancel</button>
                    <button type="submit" class="btn-primary" :disabled="isSavingLightSetup" style="padding: 0.5rem 1.25rem;">
                        <i class="ri-save-line"></i> {{ isSavingLightSetup ? 'Saving...' : 'Save Setup' }}
                    </button>
                </div>
            </form>
        </div>
      </div>
    </div>
  </Teleport>

  <!-- Dew Control Setup Modal -->
  <Teleport to="body">
    <div v-if="showDewControlModal" class="modal-overlay">
      <div class="modal-content setup-modal">
        <div class="modal-header">
            <h3 class="modal-title">
                <i class="ri-drop-line"></i> Dew Control Setup
            </h3>
            <button @click="showDewControlModal = false" class="close-btn" title="Close">
                &times;
            </button>
        </div>
        <div class="modal-body">
            <form @submit.prevent="saveDewControlSetup">
                <div class="form-group checkbox-group" style="margin-top: 0; margin-bottom: 1.2rem;">
                    <label class="toggle-switch">
                        <input type="checkbox" v-model="dewControlData.enableAutoDewControl">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label" style="font-size: 0.95rem;">Enable automatic dew control</span>
                    <i class="ri-question-line info-hint"
                       title="Automatically drives the heater from a curve based on the delta between ambient temperature and dew point, instead of a fixed manual value. A manual change to the slider on the dashboard always applies immediately, but is only temporary while Auto mode is on — the next tick overrides it again."></i>
                </div>

                <div class="form-group">
                    <label>Check Interval (Minutes)</label>
                    <input type="number" v-model.number="dewControlData.dewControlIntervalMinutes" min="1" max="1440" class="form-control">
                </div>

                <div class="data-source-box" v-if="store.liveStatus.dew_control_backend === 'external'">
                    <label style="display:block; margin-bottom: 0.6rem;">External Switch Device
                        <i class="ri-question-line info-hint" title="The Alpaca Switch device and channel that actually drives the external dew heater. Selected here after Settings.vue's backend is set to 'External Alpaca Switch'."></i>
                    </label>
                    <button type="button" class="btn-secondary" @click="discoverDewHeaterSwitches" :disabled="isDiscoveringSwitches" style="width: 100%; margin-bottom: 0.6rem;">
                        <i class="ri-radar-line"></i> {{ isDiscoveringSwitches ? 'Searching...' : 'Discover on Network' }}
                    </button>
                    <p v-if="switchDiscoveryError" class="help-text status-off" style="margin-top: -0.35rem; margin-bottom: 0.6rem; font-size: 0.8rem;">
                        ✗ {{ switchDiscoveryError }}
                    </p>
                    <div v-if="discoveredSwitches !== null" style="margin-bottom: 0.6rem;">
                        <p v-if="discoveredSwitches.length === 0" class="help-text" style="margin: 0; font-size: 0.8rem;">
                            No Switch devices found on the local network.
                        </p>
                        <div v-else style="display: flex; flex-direction: column; gap: 6px;">
                            <button v-for="device in discoveredSwitches" :key="device.baseUrl + '#' + device.deviceNumber"
                                    type="button" class="btn-secondary" @click="selectDiscoveredSwitch(device)"
                                    style="width: 100%; text-align: left; font-size: 0.85rem; padding: 0.5rem 0.75rem;">
                                <strong>{{ device.deviceName }}</strong><br>
                                <span style="opacity: 0.7; font-size: 0.75rem;">{{ device.baseUrl }} (Device {{ device.deviceNumber }})</span>
                            </button>
                        </div>
                    </div>

                    <div class="form-group" style="margin-bottom: 0.6rem;">
                        <label style="font-size: 0.85rem;">Device URL</label>
                        <input type="text" v-model="dewControlData.dewHeaterSwitchUrl" placeholder="http://192.168.1.50:11111" class="form-control">
                    </div>
                    <div class="form-group" style="margin-bottom: 0.6rem;">
                        <label style="font-size: 0.85rem;">Device Number</label>
                        <input type="number" v-model.number="dewControlData.dewHeaterSwitchDeviceNumber" min="0" class="form-control">
                    </div>
                    <button type="button" class="btn-secondary" @click="listChannelsForSelectedSwitch" :disabled="isListingChannels || !dewControlData.dewHeaterSwitchUrl" style="width: 100%;">
                        {{ isListingChannels ? 'Loading...' : 'List Channels' }}
                    </button>
                    <p v-if="switchListError" class="help-text status-off" style="margin-top: 0.5rem; margin-bottom: 0; font-size: 0.8rem;">
                        ✗ {{ switchListError }}
                    </p>

                    <div v-if="switchChannels !== null" style="margin-top: 0.6rem;">
                        <p v-if="switchChannels.length === 0" class="help-text" style="margin: 0; font-size: 0.8rem;">
                            No writable channels found on this device.
                        </p>
                        <div v-else style="display: flex; flex-direction: column; gap: 6px;">
                            <button v-for="channel in switchChannels" :key="channel.id"
                                    type="button" class="btn-secondary"
                                    :class="{ 'btn-primary': hasSelectedSwitchChannel && dewControlData.dewHeaterSwitchId === channel.id }"
                                    @click="selectSwitchChannel(channel)"
                                    style="width: 100%; text-align: left; font-size: 0.85rem; padding: 0.5rem 0.75rem;">
                                <strong>{{ channel.name }}</strong>
                                <span style="opacity: 0.7; font-size: 0.75rem;"> ({{ channel.isRheostat ? 'PWM' : 'On/Off' }}, Id {{ channel.id }})</span>
                            </button>
                        </div>
                    </div>
                    <p v-if="hasSelectedSwitchChannel" class="help-text status-on" style="margin-top: 0.6rem; margin-bottom: 0; font-size: 0.8rem;">
                        ✓ Selected: {{ selectedSwitchChannelName ? selectedSwitchChannelName + ' (Id ' + dewControlData.dewHeaterSwitchId + ')' : 'channel ' + dewControlData.dewHeaterSwitchId }} ({{ dewControlData.dewHeaterSwitchIsRheostat ? 'PWM' : 'On/Off' }})
                    </p>
                </div>

                <template v-if="store.liveStatus.dew_control_backend !== 'external' || (hasSelectedSwitchChannel && dewControlData.dewHeaterSwitchIsRheostat)">
                    <div class="form-group">
                        <label>Full-Power Delta (°C)
                            <i class="ri-question-line info-hint" title="Heater runs at 100% once temperature is within this many °C of the dew point."></i>
                        </label>
                        <input type="number" v-model.number="dewControlData.dewControlDeltaFullPower" min="-50" max="100" step="0.5" class="form-control">
                    </div>

                    <div class="form-group">
                        <label>Zero-Power Delta (°C)
                            <i class="ri-question-line info-hint" title="Heater turns off once temperature is at least this many °C above the dew point."></i>
                        </label>
                        <input type="number" v-model.number="dewControlData.dewControlDeltaZeroPower" min="-50" max="100" step="0.5" class="form-control">
                    </div>
                </template>

                <template v-if="store.liveStatus.dew_control_backend === 'external' && hasSelectedSwitchChannel && !dewControlData.dewHeaterSwitchIsRheostat">
                    <div class="form-group">
                        <label>Target Delta (°C)
                            <i class="ri-question-line info-hint" title="The switch turns on once temperature drops to within (Target - Hysteresis) °C of the dew point, and off once it rises back above (Target + Hysteresis) °C."></i>
                        </label>
                        <input type="number" v-model.number="dewControlData.dewControlOnOffTargetDelta" min="-50" max="100" step="0.5" class="form-control">
                    </div>

                    <div class="form-group">
                        <label>Hysteresis (°C)
                            <i class="ri-question-line info-hint" title="How far the delta must move past the target before the switch flips again, preventing rapid on/off cycling ('chatter') near the threshold. 0 disables hysteresis (not recommended for a real relay)."></i>
                        </label>
                        <input type="number" v-model.number="dewControlData.dewControlOnOffHysteresis" min="0" max="50" step="0.1" class="form-control">
                    </div>
                </template>

                <div class="form-group checkbox-group">
                    <label class="toggle-switch">
                        <input type="checkbox" v-model="dewControlData.dewControlOnlyWhenOpen">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label" style="font-size: 0.95rem;">Only heat while the cover is open</span>
                    <i class="ri-question-line info-hint"
                       title="Off (default): the heating curve always applies. On: the heater is forced to 0% on every tick unless the cover is currently confirmed Open (not while closed, moving, or in an unknown state) — since a closed panel already shields the optics from dew."></i>
                </div>

                <div class="form-group checkbox-group">
                    <label class="toggle-switch">
                        <input type="checkbox" v-model="dewControlData.dewControlFailsafeEnabled">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label" style="font-size: 0.95rem;">Enable failsafe on data loss</span>
                    <i class="ri-question-line info-hint"
                       title="Off (default): if no weather data is available on a tick, the heater stays at whatever it was last set to until data returns. On: forces it to Failsafe Heater Power below instead, so it can't keep running indefinitely on stale data."></i>
                </div>

                <div class="form-group checkbox-group" v-if="dewControlData.dewControlFailsafeEnabled && store.liveStatus.dew_control_backend === 'external' && hasSelectedSwitchChannel && !dewControlData.dewHeaterSwitchIsRheostat">
                    <label class="toggle-switch">
                        <input type="checkbox" :checked="dewControlData.dewControlFailsafePercent > 0" @change="dewControlData.dewControlFailsafePercent = $event.target.checked ? 100 : 0">
                        <span class="slider round"></span>
                    </label>
                    <span class="checkbox-label" style="font-size: 0.95rem;">Force switch ON on data loss</span>
                    <i class="ri-question-line info-hint" title="Whenever Auto mode has no valid weather data this tick. Off (default) leaves the switch off in that case."></i>
                </div>
                <div class="form-group" v-else-if="dewControlData.dewControlFailsafeEnabled">
                    <label>Failsafe Heater Power (%)
                        <i class="ri-question-line info-hint" title="Heater power forced whenever Auto mode has no valid weather data this tick. 0 (default) turns the heater off in that case."></i>
                    </label>
                    <input type="number" v-model.number="dewControlData.dewControlFailsafePercent" min="0" max="100" class="form-control">
                </div>

                <div class="data-source-box">
                    <div class="collapsible-header data-source-header"
                         :class="{ collapsed: !obsSectionExpanded }"
                         @click="toggleObsSection">
                        <label>Data Source 1: Alpaca ObservingConditions
                            <i class="ri-question-line info-hint" @click.stop
                               title="Queried first. If it's unreachable, or doesn't report both Temperature and Dew Point, Open-Meteo below is used instead."></i>
                        </label>
                        <span class="toggle-icon" :class="{ collapsed: !obsSectionExpanded }"></span>
                    </div>
                    <div class="collapsible-content" :class="{ collapsed: !obsSectionExpanded }">
                        <button type="button" class="btn-secondary" @click="discoverObservingConditions" :disabled="isDiscoveringObservingConditions" style="width: 100%; margin-bottom: 0.6rem;">
                            <i class="ri-radar-line"></i> {{ isDiscoveringObservingConditions ? 'Searching...' : 'Discover on Network' }}
                        </button>
                        <p v-if="discoveryError" class="help-text status-off" style="margin-top: -0.35rem; margin-bottom: 0.6rem; font-size: 0.8rem;">
                            ✗ {{ discoveryError }}
                        </p>
                        <div v-if="discoveredObservingConditions !== null" style="margin-bottom: 0.6rem;">
                            <p v-if="discoveredObservingConditions.length === 0" class="help-text" style="margin: 0; font-size: 0.8rem;">
                                No ObservingConditions devices found on the local network.
                            </p>
                            <div v-else style="display: flex; flex-direction: column; gap: 6px;">
                                <button v-for="device in discoveredObservingConditions" :key="device.baseUrl + '#' + device.deviceNumber"
                                        type="button" class="btn-secondary" @click="selectDiscoveredDevice(device)"
                                        style="width: 100%; text-align: left; font-size: 0.85rem; padding: 0.5rem 0.75rem;">
                                    <strong>{{ device.deviceName }}</strong><br>
                                    <span style="opacity: 0.7; font-size: 0.75rem;">{{ device.baseUrl }} (Device {{ device.deviceNumber }})</span>
                                </button>
                            </div>
                        </div>

                        <div class="form-group" style="margin-bottom: 0.6rem;">
                            <label style="font-size: 0.85rem;">Device URL</label>
                            <input type="text" v-model="dewControlData.observingConditionsUrl" placeholder="http://192.168.1.50:11111" class="form-control">
                        </div>
                        <div class="form-group" style="margin-bottom: 0.6rem;">
                            <label style="font-size: 0.85rem;">Device Number</label>
                            <input type="number" v-model.number="dewControlData.observingConditionsDeviceNumber" min="0" class="form-control">
                        </div>
                        <button type="button" class="btn-secondary" @click="testObservingConditionsConnection" :disabled="isTestingObservingConditions || !dewControlData.observingConditionsUrl" style="width: 100%;">
                            {{ isTestingObservingConditions ? 'Testing...' : 'Test Connection' }}
                        </button>
                        <p v-if="observingConditionsTestResult" class="help-text" :class="observingConditionsTestResult.ok ? 'status-on' : 'status-off'" style="margin-top: 0.5rem; margin-bottom: 0; font-size: 0.8rem;">
                            <template v-if="observingConditionsTestResult.ok">
                                ✓ Temperature: {{ observingConditionsTestResult.temperature }}°C, Dew Point: {{ observingConditionsTestResult.dewPoint }}°C
                            </template>
                            <template v-else>
                                ✗ {{ observingConditionsTestResult.error }}
                            </template>
                        </p>
                    </div>
                </div>

                <div class="data-source-box">
                    <div class="collapsible-header data-source-header"
                         :class="{ collapsed: !meteoSectionExpanded }"
                         @click="toggleMeteoSection">
                        <label>Data Source 2: Open-Meteo
                            <i class="ri-question-line info-hint" @click.stop
                               title="Fallback -- used only if Data Source 1 above is empty or fails."></i>
                        </label>
                        <span class="toggle-icon" :class="{ collapsed: !meteoSectionExpanded }"></span>
                    </div>
                    <div class="collapsible-content" :class="{ collapsed: !meteoSectionExpanded }">
                        <div class="form-group checkbox-group" style="margin-top: 0; margin-bottom: 0.75rem;">
                            <label class="toggle-switch">
                                <input type="checkbox" v-model="dewControlData.enableOpenMeteo">
                                <span class="slider round"></span>
                            </label>
                            <span class="checkbox-label" style="font-size: 0.95rem;">Enable Open-Meteo</span>
                        </div>
                        <div style="display: flex; gap: 10px; margin-bottom: 0.6rem;">
                            <div class="form-group" style="flex: 1; margin-bottom: 0;">
                                <label style="font-size: 0.85rem;">Latitude
                                    <i class="ri-question-line info-hint" title="Positive = North, negative = South."></i>
                                </label>
                                <input type="number" v-model.number="dewControlData.weatherLatitude" min="-90" max="90" step="0.000001" class="form-control">
                            </div>
                            <div class="form-group" style="flex: 1; margin-bottom: 0;">
                                <label style="font-size: 0.85rem;">Longitude
                                    <i class="ri-question-line info-hint" title="Positive = East, negative = West."></i>
                                </label>
                                <input type="number" v-model.number="dewControlData.weatherLongitude" min="-180" max="180" step="0.000001" class="form-control">
                            </div>
                        </div>
                        <button type="button" class="btn-secondary" @click="detectLocation" style="width: 100%;">
                            <i class="ri-map-pin-line"></i> Detect via Browser
                        </button>
                    </div>
                </div>

                <div class="form-actions" style="display: flex; gap: 10px; justify-content: flex-end; margin-top: 1.25rem;">
                    <button type="button" class="btn-secondary" @click="showDewControlModal = false" :disabled="isSavingDewControl" style="padding: 0.5rem 1rem;">Cancel</button>
                    <button type="submit" class="btn-primary" :disabled="isSavingDewControl" style="padding: 0.5rem 1.25rem;">
                        <i class="ri-save-line"></i> {{ isSavingDewControl ? 'Saving...' : 'Save Setup' }}
                    </button>
                </div>
            </form>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.dashboard-container {
    padding: 1.5rem;
}

/* Dew Control Setup's two data-source boxes (formerly inline-styled, now collapsible
   via the shared global .collapsible-header/.toggle-icon/.collapsible-content
   mechanism -- see style.css -- same one Settings.vue's "Configuration" panel and
   LiveLog.vue's "Live Log" panel already use). */
.data-source-box {
    background: rgba(0, 0, 0, 0.25);
    border: 1px solid rgba(255, 255, 255, 0.05);
    border-radius: 8px;
    padding: 0.75rem;
    margin-bottom: 1rem;
}

/* The global .collapsible-header is just a label + a thin border-bottom divider,
   sized for a top-level panel's <h2> -- against this modal's already-dim background
   that read as barely-there once collapsed (a bare gray line of text with a tiny
   chevron), inconsistent with the boxed .card-stats/.data-source-box styling used
   everywhere else on this page. Given its own tinted, rounded bar instead (same
   color-mix()-from-theme approach as .btn-secondary), so it reads as a distinct,
   clickable element in both the expanded and collapsed state. */
.data-source-header {
    padding: 0.5rem 0.6rem;
    margin-bottom: 0.6rem;
    background: color-mix(in srgb, var(--primary-color) 8%, transparent);
    border: 1px solid color-mix(in srgb, var(--primary-color) 25%, transparent);
    border-radius: 6px;
}

.data-source-header.collapsed {
    margin-bottom: 0;
}

.data-source-header:hover {
    background: color-mix(in srgb, var(--primary-color) 14%, transparent);
}

.data-source-header label {
    display: flex;
    align-items: center;
    font-size: 0.75rem;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.5px;
    color: var(--text-primary);
    margin-bottom: 0;
    cursor: pointer;
}

.data-source-header .toggle-icon {
    width: 8px;
    height: 8px;
    flex-shrink: 0;
    border-color: var(--primary-color);
}


.calibration-warning {
    background: rgba(239, 68, 68, 0.12);
    border: 1px solid rgba(239, 68, 68, 0.4);
    border-radius: 12px;
    padding: 1rem;
    margin-bottom: 1.5rem;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    color: #fca5a5;
    box-shadow: 0 4px 20px rgba(239, 68, 68, 0.15);
}

.calibration-warning-message {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    text-align: left;
}

.calibration-warning-message i {
    font-size: 1.5rem;
    color: #f87171;
}

.calibration-warning-message strong {
    display: block;
    font-size: 1rem;
    margin-bottom: 0.25rem;
    color: #f87171;
}

.calibration-warning-message span {
    font-size: 0.9rem;
    opacity: 0.9;
}

.calibration-warning-actions {
    display: flex;
    gap: 0.75rem;
    align-items: center;
}

.calibration-warning-btn {
    padding: 0.4rem 0.9rem;
    font-size: 0.85rem;
}

.calibration-warning-dismiss {
    background: none;
    border: none;
    color: #fca5a5;
    cursor: pointer;
    padding: 0.25rem;
    display: flex;
    align-items: center;
}

.calibration-warning-dismiss i {
    font-size: 1.25rem;
}

.status-grid {
    display: grid;
    grid-template-columns: 1fr;
    gap: 1.5rem;
    margin-top: 0;
}

@media (min-width: 768px) {
    .status-grid {
        grid-template-columns: repeat(2, 1fr);
    }

    /* Pro panels add a third card (Dew Heater). Rather than leaving it stranded
       under Flat Panel Light with a lopsided gap next to it, promote Light to a
       full-width header and let Cover Control + Dew Heater — two comparably
       lightweight cards — pair up evenly beneath it. Panels without a heater never
       get this class, so the plain 2-column Light|Cover layout is untouched. */
    .status-grid.has-heater > .control-card:first-child {
        grid-column: 1 / -1;
    }

    /* Lite panels have neither a cover nor a heater, so only the Light card
       renders at all -- without this it would sit alone in the first column of
       a 2-column grid, leaving an empty gap next to it (the same class of issue
       has-heater above already fixes for the 3-card case). :only-child means
       this can never conflict with has-heater's own :first-child rule -- one
       card and three cards are mutually exclusive. */
    .status-grid > .control-card:only-child {
        grid-column: 1 / -1;
    }
}

.control-card {
    background: rgba(255, 255, 255, 0.05);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 12px;
    padding: 1.5rem;
    /* Flex column so a card's primary action (Open/Close, Set Heater Power) can be
       pinned to the bottom via margin-top: auto on that one element -- otherwise a
       shorter card stretched by the grid to match a taller sibling (e.g. Cover
       Control next to Dew Heater) leaves its buttons stranded near the top with a
       big empty void below them instead of lining up with the sibling's own button. */
    display: flex;
    flex-direction: column;
}

.card-header-row {
    display: flex;
    justify-content: space-between;
    align-items: center;
    margin-bottom: 1rem;
}

.action-buttons {
    margin-top: auto;
    display: flex;
    gap: 10px;
}

.action-buttons button {
    flex: 1;
}

.control-card h3 {
    margin-top: 0;
    margin-bottom: 1rem;
    font-size: 1.2rem;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--accent-color);
}

.status-on, .status-open { color: #10b981; font-weight: bold; }
.status-off, .status-closed { color: #ef4444; font-weight: bold; }
.status-moving, .status-unknown { color: #f59e0b; font-weight: bold; }
.status-not-present { color: var(--text-secondary); font-weight: bold; }

/* Compact stat-box grid, used by all three cards' readouts (Light's
   Status/Mode/Brightness, Cover's State, Heater's Power/Mode/Ambient/Dew Point) --
   originally built just for Dew Heater to replace 4 full-width label/value rows
   that ate a lot of vertical space, then generalized once the other two cards'
   plain rows started looking visually inconsistent next to it. :only-child spans
   a lone stat (Cover's single State box) across both columns; .card-stat-wide does
   the same for a specific stat regardless of how many siblings it has (Light's
   Current Brightness, which reads oddly cramped into a half-width box). */
.card-stats {
    display: grid;
    grid-template-columns: repeat(2, 1fr);
    gap: 0.5rem 0.75rem;
    margin-bottom: 1rem;
}

.card-stats > .card-stat:only-child,
.card-stats > .card-stat-wide {
    grid-column: 1 / -1;
}

.card-stat {
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
    padding: 0.5rem 0.75rem;
    background: rgba(255, 255, 255, 0.03);
    border: 1px solid rgba(255, 255, 255, 0.08);
    border-radius: 6px;
}

.card-stat-label {
    font-size: 0.7rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--text-secondary);
}

.card-stat-value {
    font-size: 1.05rem;
    font-weight: 600;
    color: var(--text-primary);
}

/* Dew Heater's Mode value is the only stat that carries a tooltip (data source +
   the manual-override-is-temporary caveat) -- cursor: help signals it's worth
   hovering, now that it's this stat's only way of surfacing that information. */
.card-stat-value.status-on {
    cursor: help;
}

.brightness-slider-container {
    display: flex;
    align-items: center;
    gap: 1rem;
}

.brightness-slider {
    flex: 1;
    width: 100%;
    box-sizing: border-box;
}

.brightness-slider-input {
    width: 80px;
    padding: 0.4rem;
    font-size: 0.9rem;
    text-align: center;
    background: rgba(0, 0, 0, 0.3);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 4px;
    color: white;
    box-sizing: border-box;
}

.brightness-slider-input::-webkit-outer-spin-button,
.brightness-slider-input::-webkit-inner-spin-button {
    -webkit-appearance: none;
    margin: 0;
}

.brightness-slider-input {
    -moz-appearance: textfield;
}

.jog-controls {
    display: flex;
    flex-wrap: nowrap;
    gap: 0.35rem;
    justify-content: center;
    margin-top: 1rem;
}

/* Selector used to be ".jog-controls .btn" -- a class these buttons never actually
   carried (they're "btn-secondary", from before this project ever had a ".btn" base
   class combined with a variant), so none of this ever applied: the 8 jog buttons
   rendered at .btn-secondary's full default padding (0.8rem 1.5rem) with nothing to
   shrink them, overflowing a nowrap flex row well past the modal's width and getting
   clipped at both edges (spotted from a screenshot). Pre-existing since the very first
   commit, not something recent broke. */
.jog-controls .btn-secondary {
    flex: 1;
    min-width: 0;
    padding: 0.5rem 0.1rem;
    font-size: 0.8rem;
    white-space: nowrap;
}

.help-text {
    font-size: 0.9rem;
    opacity: 0.8;
    margin-bottom: 1rem;
}

.calibrate-link {
    background: none;
    border: none;
    color: var(--accent-color);
    font-size: 0.85rem;
    cursor: pointer;
    display: flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.25rem 0.5rem;
    border-radius: 4px;
    transition: all 0.2s ease;
}
.calibrate-link:hover:not(:disabled) {
    color: var(--text-primary);
    background: rgba(255, 255, 255, 0.05);
}
.calibrate-link:disabled {
    opacity: 0.5;
    cursor: not-allowed;
}

.form-group {
    margin-bottom: 1.2rem;
}

.form-group label {
    display: block;
    margin-bottom: 0.5rem;
    font-size: 0.9rem;
    color: var(--text-secondary);
}

.form-control {
    width: 100%;
    padding: 0.65rem 0.75rem;
    background: rgba(0, 0, 0, 0.25);
    border: 1px solid rgba(255, 255, 255, 0.1);
    border-radius: 6px;
    color: white;
    font-family: inherit;
    font-size: 0.95rem;
    box-sizing: border-box;
    transition: border-color 0.2s ease, box-shadow 0.2s ease;
}

.form-control:focus {
    outline: none;
    border-color: var(--accent-color);
    box-shadow: 0 0 0 2px rgba(61, 138, 122, 0.2);
}

.checkbox-group {
    display: flex;
    align-items: center;
    gap: 10px;
}

.checkbox-label {
    font-size: 0.95rem;
}
</style>
