<script setup lang="ts">
// A share of players, as the API stores it — hundredths of a percent, 0 to 10,000 — and as a
// person sets it: a slider for the gesture, a field for the exact figure, and the steps a
// rollout usually climbs (1, 5, 10, 25, 50, 100) one press away. Which players are in is
// decided by a hash of the install, so raising the share keeps everybody already in.
withDefaults(defineProps<{
  /** What the share is of, for a screen reader: "Rule 2 rollout". */
  label: string
  disabled?: boolean
}>(), {
  disabled: false
})

const rollout = defineModel<number>({ required: true })

const STEPS = [100, 500, 1000, 2500, 5000, FULL_ROLLOUT]

const text = ref('')
watch(rollout, (value) => {
  text.value = String(value / 100)
}, { immediate: true })

function commit() {
  const parsed = Number(text.value.replace('%', '').replace(',', '.'))
  if (!Number.isFinite(parsed)) {
    text.value = String(rollout.value / 100)
    return
  }
  rollout.value = Math.round(Math.min(100, Math.max(0, parsed)) * 100)
}

const slider = computed({
  get: () => rollout.value / 100,
  set: (value: number) => {
    rollout.value = Math.round(value * 100)
  }
})

const id = useId()
</script>

<template>
  <div class="flex flex-col gap-2">
    <div class="flex flex-wrap items-center gap-3">
      <v-slider
        v-model="slider"
        :aria-label="label"
        :disabled="disabled"
        class="ps-rollout-field__slider min-w-40 flex-1"
        color="primary"
        density="compact"
        hide-details
        max="100"
        min="0"
        step="1"
        thumb-size="14"
        track-size="4"
      />
      <div class="flex items-center gap-1">
        <v-text-field
          :id="id"
          v-model="text"
          :aria-label="`${label}, percent`"
          :disabled="disabled"
          class="ps-rollout-field__input w-20 flex-none"
          density="compact"
          hide-details
          inputmode="decimal"
          variant="outlined"
          @blur="commit"
          @keydown.enter.prevent="commit"
        />
        <span class="text-xs text-ink/55">%</span>
      </div>
    </div>
    <div class="flex flex-wrap gap-1">
      <button
        v-for="step in STEPS"
        :key="step"
        :aria-pressed="rollout === step"
        :class="rollout === step ? 'bg-primary/10 text-primary' : 'text-ink/55 hover:bg-ink/5 hover:text-ink'"
        :disabled="disabled"
        class="rounded-(--ps-radius-x-small) px-1.5 py-0.5 font-mono text-[11px] transition-colors duration-150 disabled:opacity-40"
        type="button"
        @click="rollout = step"
      >
        {{ formatRollout(step) }}
      </button>
    </div>
  </div>
</template>

<style scoped>
.ps-rollout-field__input :deep(input) {
  font-family: var(--font-mono);
  text-align: right;
}
</style>
