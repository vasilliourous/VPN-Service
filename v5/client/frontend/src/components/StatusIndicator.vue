<template>
  <div :class="['indicator', state]">
    <div class="dot"></div>
  </div>
</template>

<script setup lang="ts">
defineProps<{
  state: 'connected' | 'disconnected' | 'error'
}>()
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

.error .dot {
  background: #EF4444;
  box-shadow: 0 0 8px rgba(239, 68, 68, 0.5);
}
</style>
