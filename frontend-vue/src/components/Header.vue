<script setup>
import { ref } from 'vue'
import { useDeviceStore } from '../stores/device'
import { useThemeStore } from '../stores/theme'
import { useModalStore } from '../stores/modal'
import { storeToRefs } from 'pinia'

const store = useDeviceStore()
const themeStore = useThemeStore()
const modal = useModalStore()
const { firmwareVersion, comPort, connectionStatus, isConnected, isIdleOnDemand, proxyVersion, proxyConfig } = storeToRefs(store)
const { currentTheme } = storeToRefs(themeStore)
const { THEMES } = themeStore

const handleThemeChange = (event) => {
    themeStore.setTheme(event.target.value)
}

// Connect-on-demand mode's manual trigger -- only shown while isIdleOnDemand (see
// stores/device.js's checkConnection), i.e. deliberately dormant rather than actively
// trying/failing on its own. The request itself takes as long as the real handshake
// (up to ~15s on the backend), so this shows its own "Connecting..." state rather than
// relying on the header's usual poll-driven one.
const isManuallyConnecting = ref(false)

async function handleManualConnect() {
    isManuallyConnecting.value = true
    try {
        const result = await store.manualConnect()
        if (!result.connected) {
            modal.error('Could not connect to the panel — check that it is plugged in and powered on.')
        }
    } catch (e) {
        modal.error('Failed to connect: ' + e.message)
    } finally {
        isManuallyConnecting.value = false
    }
}

// Connect-on-demand mode's manual release -- the counterpart to handleManualConnect,
// shown instead of it once connected (only in this mode; in the default always-on mode
// the background loop would just reconnect again within a few seconds regardless).
const isManuallyDisconnecting = ref(false)

async function handleManualDisconnect() {
    isManuallyDisconnecting.value = true
    try {
        await store.manualDisconnect()
    } catch (e) {
        modal.error('Failed to disconnect: ' + e.message)
    } finally {
        isManuallyDisconnecting.value = false
    }
}
</script>

<template>
  <header class="main-header glass-panel">
      <div class="header-content">
          <h1>Gemini Flat Panel</h1>
          <span class="subtitle">Alpaca Driver & Controller <span id="proxy-version-display">{{ proxyVersion }}</span></span>
      </div>
      <div class="header-badges">
          <div class="theme-selector">
              <label for="theme-select" class="theme-label">
                  <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                      <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"></path>
                  </svg>
              </label>
              <select id="theme-select" v-model="currentTheme" @change="handleThemeChange" class="theme-select">
                  <option :value="THEMES.DARK">Material Dark</option>
                  <option :value="THEMES.DEFAULT">Deep Space</option>
                  <option :value="THEMES.RED">Night Vision</option>
              </select>
          </div>
          <div class="status-badge" id="com-port-badge">
              <span class="value">{{ comPort }}</span>
          </div>
          <div class="status-badge" id="revision-badge">
              <span class="label">REV</span>
              <span class="value">{{ store.liveStatus.panel_revision }}</span>
          </div>
          <div class="status-badge" id="firmware-badge">
              <span class="label">FW</span>
              <span class="value">{{ firmwareVersion }}</span>
          </div>
          <div class="connection-pill" id="connection-status-pill">
              <span id="connection-indicator" :class="{ connected: isConnected, paused: isIdleOnDemand, disconnected: !isConnected && !isIdleOnDemand }"></span>
              <span id="connection-text">{{ connectionStatus }}</span>
              <button v-if="isIdleOnDemand" class="pill-action-btn" @click="handleManualConnect" :disabled="isManuallyConnecting" title="Actively try to connect now">
                  {{ isManuallyConnecting ? 'Connecting…' : 'Connect' }}
              </button>
              <button v-else-if="isConnected && proxyConfig.serialConnectOnDemand" class="pill-action-btn" @click="handleManualDisconnect" :disabled="isManuallyDisconnecting" title="Release the port back to dormant">
                  {{ isManuallyDisconnecting ? 'Disconnecting…' : 'Disconnect' }}
              </button>
          </div>
      </div>
  </header>
</template>

<style scoped>
/* Compact enough to sit inside the connection-pill's own pill shape (0.5rem 1rem
   padding, 50px radius) rather than looking like a full-size .btn-secondary dropped
   into a small space -- same accent-tinted approach (color-mix() from
   --primary-color) as .btn-secondary itself, just scaled down. */
.pill-action-btn {
    background: color-mix(in srgb, var(--primary-color) 10%, transparent);
    border: 1px solid var(--primary-color);
    color: var(--primary-color);
    border-radius: 50px;
    padding: 0.2rem 0.7rem;
    font-size: 0.8rem;
    cursor: pointer;
    margin-left: 0.2rem;
}

.pill-action-btn:hover:not(:disabled) {
    background: var(--primary-color);
    color: #0f0c29;
}

.pill-action-btn:disabled {
    opacity: 0.6;
    cursor: default;
}

.theme-selector {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    background: var(--surface-color);
    padding: 0.5rem 0.75rem;
    border-radius: 50px;
    border: 1px solid var(--surface-border);
    transition: all 0.3s ease;
}

.theme-selector:hover {
    background: var(--surface-hover);
    border-color: rgba(255, 255, 255, 0.2);
}

.theme-selector:focus-within {
    border-color: var(--primary-color);
    box-shadow: 0 0 0 2px rgba(0, 210, 255, 0.2);
    background: rgba(255, 255, 255, 0.1);
}

.theme-label {
    display: flex;
    align-items: center;
    margin: 0;
    color: var(--text-secondary);
    cursor: pointer;
}

.theme-label svg {
    transition: transform 0.3s ease;
}

.theme-selector:hover .theme-label svg {
    transform: rotate(20deg);
}

.theme-select {
    background: rgba(255, 255, 255, 0.01); /* Nearly invisible, for Firefox color-scheme support */
    border: none;
    color: var(--text-primary);
    font-size: 0.9rem;
    cursor: pointer;
    outline: none;
    padding: 0;
    font-family: inherit;
    font-weight: 500;
    color-scheme: dark;
    box-shadow: none !important; /* Disable individual focus shadow */
}
</style>
