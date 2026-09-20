<script setup>
import licenseText from '../../../LICENSE?raw'

defineEmits(['close'])
</script>

<template>
  <div class="modal-overlay" @click.self="$emit('close')">
    <div class="modal-content license-modal">
      <div class="modal-header">
        <h3 class="modal-title">License</h3>
        <button @click="$emit('close')" class="close-btn" title="Close">&times;</button>
      </div>
      <div class="modal-body">
        <p class="description-text">Gemini Flat Panel is licensed under the GNU General Public License v3.0.</p>
        <div class="license-text-scroll"><pre>{{ licenseText }}</pre></div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Deliberately does NOT redefine .modal-overlay/.modal-content - the global versions
   (assets/css/style.css) already give the right backdrop/z-index/theming. */
.license-modal {
    max-width: 700px;
    width: 90%;
    text-align: left;
}

.description-text {
    color: var(--text-secondary);
    margin: 0 0 0.75rem;
}

.license-text-scroll {
    max-height: 50vh;
    overflow-y: auto;
    white-space: pre-wrap;
    word-break: break-word;
    font-family: monospace;
    font-size: 0.78rem;
    line-height: 1.4;
    background: rgba(0, 0, 0, 0.15);
    border: 1px solid var(--surface-border);
    border-radius: var(--radius-sm);
    padding: 1rem;
}

.license-text-scroll pre {
    margin: 0;
    white-space: inherit;
    font-family: inherit;
}
</style>
