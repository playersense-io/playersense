<script setup lang="ts">
import type { BadgeTone } from '../utils/components'
/**
 * A word in a small outlined pill with a coloured dot before it, for a state a table
 * cell has to name: active, suspended, deleted. The dot carries the tone and the word
 * carries the meaning, so a reader who cannot tell the colours apart loses nothing, and
 * the pill itself is the same in every tone so a column of them reads as one column.
 */
withDefaults(
  defineProps<{
    label: string
    tone?: BadgeTone
    /** For a detail after the word, set quieter: "until Oct 3". */
    detail?: string
  }>(),
  { tone: 'neutral', detail: '' }
)

const DOTS: Record<BadgeTone, string> = {
  success: 'bg-success',
  warning: 'bg-warning',
  error: 'bg-error',
  neutral: 'bg-ink/30'
}
</script>

<template>
  <span
    class="inline-flex h-5 max-w-full items-center gap-1.5 rounded-full border border-line bg-surface px-2 text-[11px] leading-none font-medium whitespace-nowrap text-ink/80"
  >
    <span :class="DOTS[tone]" aria-hidden="true" class="size-1.5 flex-none rounded-full" />
    <span class="truncate">{{ label }}</span>
    <span v-if="detail" class="truncate font-mono font-normal text-ink/50">{{ detail }}</span>
  </span>
</template>
