<script setup lang="ts">
/**
 * Sets one bounded whole number: a length floor, an attempt count. The row shows the
 * value and this takes the new one, so a figure on its way to two digits is never
 * written, and the bounds are checked here with the reason beside the field. The
 * labels come from the caller, because "8 characters" and "3 attempts" are different
 * sentences about the same shape of rule.
 */
const props = withDefaults(
  defineProps<{
    title: string
    description?: string
    label: string
    suffix?: string
    min: number
    max: number
    current: number
    /** Shown under the field while the value stands. */
    hint?: string
    /** The reasons a value is refused: not a whole number, or outside the bounds. */
    integerLabel: string
    rangeLabel: string
    confirmLabel?: string
  }>(),
  { description: '', suffix: '', hint: '', confirmLabel: 'Change' }
)

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{
  confirm: [value: number]
}>()

const text = ref('')
/* Focused once the dialog has finished entering; `autofocus` loses to the overlay's own focus. */
const field = useTemplateRef<{ focus: () => void }>('field')

watch(open, (value) => {
  if (value) text.value = String(props.current)
})

const value = computed(() => Number(text.value.trim()))
const unchanged = computed(() => value.value === props.current)

const issue = computed(() => {
  if (text.value.trim() === '' || !Number.isInteger(value.value)) return props.integerLabel
  if (value.value < props.min || value.value > props.max) return props.rangeLabel
  return ''
})

const ready = computed(() => issue.value === '' && !unchanged.value)
const id = `${useId()}-value`

function confirm() {
  if (!ready.value) return
  emit('confirm', value.value)
  open.value = false
}
</script>

<template>
  <v-dialog v-model="open" max-width="440" @after-enter="field?.focus()">
    <v-card border flat>
      <form class="flex flex-col gap-5 p-5" @submit.prevent="confirm">
        <header class="flex flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            {{ title }}
          </h2>
          <p v-if="description" class="m-0 text-xs leading-5 text-ink/60">
            {{ description }}
          </p>
        </header>

        <div class="flex flex-col gap-1">
          <label :for="id" class="text-xs font-medium text-ink">{{ label }}</label>
          <v-text-field
            :id="id"
            ref="field"
            v-model="text"
            :error="issue !== ''"
            :max="max"
            :min="min"
            :suffix="suffix"
            autocomplete="off"
            class="font-mono"
            hide-details
            type="number"
            variant="filled"
          />
          <p :class="issue ? 'text-error' : 'text-ink/50'" class="m-0 text-[11px]">
            {{ issue || hint || `Between ${min} and ${max}.` }}
          </p>
        </div>

        <footer class="flex items-center justify-end gap-2">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Cancel
          </v-btn>
          <v-btn
            :disabled="!ready"
            class="text-xs font-medium"
            color="primary"
            size="small"
            type="submit"
            variant="flat"
          >
            {{ confirmLabel }}
          </v-btn>
        </footer>
      </form>
    </v-card>
  </v-dialog>
</template>
