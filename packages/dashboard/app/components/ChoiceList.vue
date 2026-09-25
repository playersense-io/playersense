<script setup lang="ts">
import type { ChoiceOption } from '../utils/components'
/**
 * One choice out of a few, each able to carry the sentence that says what it means: a radio
 * group in ARIA's terms and the keyboard's, drawn as rows. For a choice whose options need
 * explaining, where a segmented control has no room for the sentence.
 *
 * One tab stop, arrows to move, and selection follows the arrow as it does in a native
 * group. Nothing is chosen until somebody chooses, unless the caller starts on a value: a
 * preselected reason is a reason nobody gave.
 */
const props = withDefaults(
  defineProps<{
    options: readonly ChoiceOption[]
    /** What is being chosen, for a screen reader. */
    label: string
    /** Two columns from `sm`, for short options without notes. */
    columns?: 1 | 2
    disabled?: boolean
    error?: boolean
  }>(),
  { columns: 1, disabled: false, error: false }
)

const model = defineModel<string | null>({ required: true })

const group = useTemplateRef<HTMLElement>('group')

/* Wraps at either end, as a native radio group does. */
const KEYS: Record<string, 1 | -1> = { ArrowDown: 1, ArrowRight: 1, ArrowUp: -1, ArrowLeft: -1 }

function move(event: KeyboardEvent, from: number) {
  const step = KEYS[event.key]
  if (step === undefined) return
  event.preventDefault()
  const next = (from + step + props.options.length) % props.options.length
  const option = props.options[next]
  if (option === undefined) return
  model.value = option.value
  group.value?.querySelectorAll<HTMLButtonElement>('[role="radio"]')[next]?.focus()
}

/* The chosen option is the tab stop, or the first one while nothing is chosen. */
function tabIndex(value: string, index: number): number {
  if (model.value === value) return 0
  const chosen = props.options.some(option => option.value === model.value)
  return !chosen && index === 0 ? 0 : -1
}

function frame(value: string): string {
  if (model.value === value) return 'border-primary bg-primary/6'
  return props.error ? 'border-error/60 bg-surface hover:bg-ink/3' : 'border-line bg-surface hover:bg-ink/3'
}
</script>

<template>
  <div
    ref="group"
    :aria-invalid="error || undefined"
    :aria-label="label"
    :class="columns === 2 ? 'sm:grid-cols-2' : ''"
    class="grid gap-1.5"
    role="radiogroup"
  >
    <button
      v-for="(option, index) in options"
      :key="option.value"
      :aria-checked="model === option.value"
      :class="frame(option.value)"
      :disabled="disabled"
      :tabindex="tabIndex(option.value, index)"
      class="flex cursor-pointer items-start gap-2.5 rounded-(--ps-radius-small) border px-3 py-2.5 text-left transition-colors duration-150 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-primary disabled:cursor-default disabled:opacity-60"
      role="radio"
      type="button"
      @click="model = option.value"
      @keydown="move($event, index)"
    >
      <span
        :class="model === option.value ? 'border-primary' : 'border-ink/25'"
        aria-hidden="true"
        class="mt-0.5 flex size-3.5 flex-none items-center justify-center rounded-full border-2"
      >
        <span v-if="model === option.value" class="size-1.5 rounded-full bg-primary" />
      </span>
      <Icon
        v-if="option.icon"
        :name="option.icon"
        :aria-hidden="true"
        class="mt-px flex-none text-sm text-ink/60"
      />
      <span class="flex min-w-0 flex-col gap-0.5">
        <span class="text-xs font-medium text-ink">{{ option.label }}</span>
        <span v-if="option.note" class="text-[11px] leading-4 text-ink/55">{{ option.note }}</span>
      </span>
    </button>
  </div>
</template>
