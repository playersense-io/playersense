<script setup lang="ts">
// A dollar amount typed in, held as micros: what every money field on Billing takes. The text
// is kept apart from the value, so "12." on its way to "12.50" is not redrawn as "12".
const props = withDefaults(defineProps<{
  label: string
  error?: boolean
  placeholder?: string
}>(), {
  error: false,
  placeholder: '0.00'
})

const micros = defineModel<number>({ required: true })

const text = ref(micros.value ? String(micros.value / MICROS) : '')
watch(micros, (value) => {
  if (Math.round(Number(text.value || 0) * MICROS) !== value) text.value = value ? String(value / MICROS) : ''
})

function typed(next: string | null) {
  text.value = (next ?? '').replace(/[^\d.]/g, '')
  const value = Number(text.value)
  micros.value = Number.isFinite(value) ? Math.round(value * 100) * (MICROS / 100) : 0
}
</script>

<template>
  <v-text-field
    :aria-label="props.label"
    :error="error"
    :model-value="text"
    :placeholder="placeholder"
    class="ps-money-field"
    density="comfortable"
    hide-details
    inputmode="decimal"
    prefix="$"
    variant="outlined"
    @update:model-value="typed"
  />
</template>

<style scoped>
.ps-money-field :deep(input) {
  font-family: var(--font-mono);
}
</style>
