<script setup lang="ts">
// Several choices out of a few, each able to carry the sentence that says what it means: the
// checkbox version of `ChoiceList`, drawn the same way, so a form that asks "which one" and
// "which of these" reads as one form. A choice that cannot be made here says why, in place of
// its note, rather than disappearing.
withDefaults(defineProps<{
  options: readonly (ChoiceOption & { disabledReason?: string })[]
  /** What is being chosen, for a screen reader. */
  label: string
  /** Two columns from `sm`. */
  columns?: 1 | 2
}>(), {
  columns: 1
})

const model = defineModel<string[]>({ required: true })

function toggle(value: string) {
  model.value = model.value.includes(value) ? model.value.filter(entry => entry !== value) : [...model.value, value]
}

function frame(value: string, disabled: boolean): string {
  if (disabled) return 'border-line bg-ink/2 opacity-60'
  return model.value.includes(value) ? 'border-primary bg-primary/6' : 'border-line bg-surface hover:bg-ink/3'
}
</script>

<template>
  <div
    :aria-label="label"
    :class="columns === 2 ? 'sm:grid-cols-2' : ''"
    class="grid gap-1.5"
    role="group"
  >
    <button
      v-for="option in options"
      :key="option.value"
      :aria-pressed="model.includes(option.value)"
      :class="frame(option.value, Boolean(option.disabledReason))"
      :disabled="Boolean(option.disabledReason)"
      class="flex cursor-pointer items-start gap-2.5 rounded-(--ps-radius-small) border px-3 py-2.5 text-left transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:cursor-default"
      type="button"
      @click="toggle(option.value)"
    >
      <span
        :class="model.includes(option.value) ? 'border-primary bg-primary text-white' : 'border-ink/25'"
        aria-hidden="true"
        class="mt-0.5 flex size-3.5 flex-none items-center justify-center rounded-(--ps-radius-x-small) border-2"
      >
        <Icon v-if="model.includes(option.value)" class="text-[11px]" name="material-symbols:check" />
      </span>
      <span class="flex min-w-0 flex-col gap-0.5">
        <span class="text-xs font-medium text-ink">{{ option.label }}</span>
        <span class="text-[11px] leading-4 text-ink/55">{{ option.disabledReason || option.note }}</span>
      </span>
    </button>
  </div>
</template>
