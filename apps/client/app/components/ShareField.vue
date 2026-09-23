<script setup lang="ts">
// A share of an audience as one field in a sentence — "to [25 %] of them" — stored as the API
// stores it, hundredths of a percent. Typed, or picked from the steps a rollout usually climbs;
// a figure that is not one is put back to what it was when the field is left.
defineProps<{
  /** Names the field for a screen reader: "Rule 2 share". */
  label: string
}>()

const rollout = defineModel<number>({ required: true })

/* The field's suffix says percent, so an option is the bare figure it will read as. */
const STEPS = ['1', '5', '10', '25', '50', '100']

const shown = (value: number) => String(Number((value / 100).toFixed(2)))

const text = ref(shown(rollout.value))
watch(rollout, (value) => {
  text.value = shown(value)
})

function commit(next: string | null) {
  const parsed = Number(String(next ?? '').replace('%', '').replace(',', '.').trim())
  if (next === null || String(next).trim() === '' || !Number.isFinite(parsed)) {
    text.value = shown(rollout.value)
    return
  }
  rollout.value = Math.round(Math.min(100, Math.max(0, parsed)) * 100)
  text.value = shown(rollout.value)
}
</script>

<template>
  <v-combobox
    v-model="text"
    v-bind="FIELD_MENU"
    :aria-label="label"
    :items="STEPS"
    :return-object="false"
    class="ps-share-field w-28 flex-none"
    density="comfortable"
    hide-details
    inputmode="decimal"
    suffix="%"
    variant="outlined"
    @blur="commit(text)"
    @keydown.enter.prevent="commit(text)"
    @update:model-value="(value: string | null) => value !== null && STEPS.includes(value) && commit(value)"
  />
</template>

<style scoped>
.ps-share-field :deep(input) {
  font-family: var(--font-mono);
}
</style>
