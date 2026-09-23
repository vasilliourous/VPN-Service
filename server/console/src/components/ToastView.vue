<script setup lang="ts">
import { toast } from '../toast'
</script>

<template>
  <div class="toast">
    <div
      v-for="t in toast.state.items"
      :key="t.id"
      class="msg"
      :class="t.kind"
      style="margin-bottom: 8px; cursor: pointer"
      @click="toast.dismiss(t.id)"
    >
      {{ t.text }}
    </div>
  </div>
</template>
