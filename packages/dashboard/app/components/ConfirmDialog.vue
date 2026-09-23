<script setup lang="ts">
/**
 * The question before an action that cannot be taken back: what will happen, in a
 * sentence, and a button that names the outcome — "Delete funnel", never "Confirm" — so
 * the reader who only reads the button still knows what they are agreeing to.
 *
 * Cancelling is the safe answer and sits left, and it is where focus lands, so an Enter
 * pressed out of habit keeps what was there. The action wears the error colour when it
 * destroys something.
 */
withDefaults(defineProps<{
  title: string
  description?: string
  /** Names the outcome: "Delete funnel". */
  action: string
  tone?: 'error' | 'primary'
}>(), {
  description: '',
  tone: 'error'
})

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{ confirm: [] }>()

const cancel = useTemplateRef<{ $el: HTMLElement }>('cancel')

function confirm() {
  emit('confirm')
  open.value = false
}
</script>

<template>
  <v-dialog v-model="open" max-width="420" @after-enter="cancel?.$el.focus()">
    <v-card border flat>
      <div class="flex flex-col gap-5 p-5">
        <header class="flex flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            {{ title }}
          </h2>
          <p v-if="description" class="m-0 text-xs leading-5 text-ink/60">
            {{ description }}
          </p>
        </header>

        <footer class="flex items-center justify-end gap-2">
          <v-btn
            ref="cancel"
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Cancel
          </v-btn>
          <v-btn
            :color="tone"
            class="text-xs font-medium"
            size="small"
            variant="flat"
            @click="confirm"
          >
            {{ action }}
          </v-btn>
        </footer>
      </div>
    </v-card>
  </v-dialog>
</template>
