<script setup lang="ts">
/**
 * The line at the foot of a settings screen that saves itself: whether a write is in
 * flight, and otherwise when the last one landed. Centred and quiet, because it is
 * reassurance and not a control.
 *
 * The relative time is measured against the later of the clock and the stamp. The
 * stamp is written after the tick that preceded it, and a clock behind the stamp reads
 * "in a few seconds" for a moment before flipping.
 */
import { formatDateTime, relativeTimeWords } from '../utils/time'

const props = defineProps<{
  saving: boolean
  lastSaved: string | null
}>()

const now = useNow({ interval: 1000 })

const savedAgo = computed(() => {
  if (props.lastSaved === null) return ''
  const stamp = new Date(props.lastSaved)
  return relativeTimeWords(props.lastSaved, now.value < stamp ? stamp : now.value)
})
</script>

<template>
  <p
    class="m-0 flex items-center justify-center gap-2 px-1 text-[11px] leading-4 text-ink/50"
    role="status"
  >
    <template v-if="saving">
      <span class="leading-none">Saving</span>
      <Icon :aria-hidden="true" class="-mx-1.5 flex-none text-xl text-ink/60" name="lucide:dot" />
      <span class="leading-none">a moment</span>
    </template>
    <template v-else-if="lastSaved">
      <span class="leading-none">Last saved</span>
      <Icon :aria-hidden="true" class="-mx-1.5 flex-none text-xl text-ink/60" name="lucide:dot" />
      <v-tooltip :text="formatDateTime(lastSaved)" location="top">
        <template #activator="{ props: tip }">
          <span v-bind="tip" class="leading-none text-ink/70">{{ savedAgo }}</span>
        </template>
      </v-tooltip>
    </template>
    <template v-else>
      <span class="leading-none">Nothing changed</span>
      <Icon :aria-hidden="true" class="-mx-1.5 flex-none text-xl text-ink/60" name="lucide:dot" />
      <span class="leading-none">this session</span>
    </template>
  </p>
</template>
