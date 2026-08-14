<template>
  <div :class="['badge', tier]" role="img" :aria-label="`Tier: ${label}`">
    <span class="badge-icon" aria-hidden="true">{{ icon }}</span>
    <span class="badge-text">{{ label }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{
  tier: string
}>()

const icon = computed(() => {
  switch (props.tier) {
    case 'strike':  return '⚡'
    case 'stealth': return '◉'
    default:        return '○'
  }
})

// Safe label that never throws on an empty/unknown tier.
const label = computed(() => {
  const t = props.tier || 'unknown'
  return t.charAt(0).toUpperCase() + t.slice(1)
})
</script>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 4px 10px;
  border-radius: 20px;
  font-size: 12px;
  font-weight: 600;
}

.eco {
  background: rgba(107, 114, 128, 0.2);
  color: #9CA3AF;
}

.stealth {
  background: rgba(168, 85, 247, 0.2);
  color: #A855F7;
}

.strike {
  background: rgba(234, 179, 8, 0.2);
  color: #EAB308;
}

.badge-icon {
  font-size: 11px;
}
</style>
