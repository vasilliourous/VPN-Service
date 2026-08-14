<template>
  <div
    :class="['indicator', state]"
    role="status"
    :aria-label="statusText"
    :title="statusText"
  >
    <div class="dot"></div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  state: 'connected' | 'disconnected' | 'error' | 'degraded'
}>()

const statusText = computed(() => {
  switch (props.state) {
    case 'connected': return 'Connected'
    case 'degraded': return 'Connection being repaired'
    case 'error': return 'Error'
    default: return 'Disconnected'
  }
})
</script>

<style scoped>
.indicator {
  display: flex;
  align-items: center;
}

.dot {
  width: 10px;
  height: 10px;
  border-radius: 50%;
  transition: background 0.3s, box-shadow 0.3s;
}

.connected .dot {
  background: #22C55E;
  box-shadow: 0 0 8px rgba(34, 197, 94, 0.5);
}

.disconnected .dot {
  background: #6B7280;
}

.degraded .dot {
  background: #F59E0B;
  box-shadow: 0 0 8px rgba(245, 158, 11, 0.5);
}

.error .dot {
  background: #EF4444;
  box-shadow: 0 0 8px rgba(239, 68, 68, 0.5);
}
</style>
