<script setup lang="ts">
// Names one thing: a funnel being saved as new, or one being renamed. The shape of
// `NumberDialog`, for a line of text: the field is filled with the current name and
// focused once the dialog has entered, and an empty or unchanged name cannot be confirmed.
const props = withDefaults(defineProps<{
  title: string
  description?: string
  label?: string
  current?: string
  confirmLabel?: string
}>(), {
  description: '',
  label: 'Name',
  current: '',
  confirmLabel: 'Save'
})

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{
  confirm: [name: string]
}>()

const text = ref('')
const field = useTemplateRef<{ focus: () => void }>('field')

watch(open, (value) => {
  if (value) text.value = props.current
})

const name = computed(() => text.value.trim())
const ready = computed(() => name.value !== '' && name.value !== props.current)
const id = `${useId()}-name`

function confirm() {
  if (!ready.value) return
  emit('confirm', name.value)
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
            autocomplete="off"
            hide-details
            maxlength="60"
            variant="filled"
          />
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
