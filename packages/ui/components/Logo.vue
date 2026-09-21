<script setup lang="ts">
import LogoMark from './LogoMark.vue'

withDefaults(defineProps<{ size?: number | string }>(), { size: 22 })
</script>

<template>
  <span class="inline-flex items-center gap-1.5 text-neutral-950 dark:text-neutral-50">
    <LogoMark :size="size" />
    <span class="wordmark text-base font-semibold tracking-tight">playersense</span>
  </span>
</template>

<style scoped>
.wordmark {
  font-family: 'Space Grotesk', ui-sans-serif, sans-serif;
}
</style>
