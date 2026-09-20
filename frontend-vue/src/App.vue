<script setup>
import { onMounted, ref } from 'vue'
import Header from './components/Header.vue'
import Dashboard from './components/Dashboard.vue'
import Settings from './components/Settings.vue'
import LiveLog from './components/LiveLog.vue'
import AppModal from './components/AppModal.vue'
import Footer from './components/Footer.vue'
import { useDeviceStore } from './stores/device'
import { useThemeStore } from './stores/theme'

const store = useDeviceStore()
const themeStore = useThemeStore()

onMounted(() => {
    store.startPolling()
    // Theme is automatically applied via the theme store initialization
})
</script>

<template>
  <div class="container">
    <!-- Global Modal -->
    <AppModal />
    
    <Header />
    
    <main>
      <div class="dashboard-grid">
        <Dashboard />
      </div>

      <div class="section-spacing">
        <Settings />
      </div>
      
      <!-- Live Log Section -->
      <div class="section-spacing">
        <LiveLog />
      </div>
    </main>

    <Footer />
  </div>
</template>

<style scoped>
.section-spacing {
    margin-top: 1.5rem;
}
</style>
