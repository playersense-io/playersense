<script setup lang="ts">
import type { SegmentMarkValue } from '../utils/components'
/**
 * The small picture beside a segment's name, for the dimensions that have one: a country's
 * flag, a platform's mark. One component so a table or a legend asks for "the mark of this
 * segment" and never has to know which kind it is.
 */
withDefaults(defineProps<{
  mark: SegmentMarkValue
  size?: number
}>(), {
  size: 14
})
</script>

<template>
  <CountryFlag v-if="mark.kind === 'country'" :code="mark.code" :size="size" />
  <PlatformIcon v-else :platform="mark.code" :size="size" />
</template>
