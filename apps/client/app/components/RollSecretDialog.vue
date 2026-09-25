<script setup lang="ts">
// A new signing secret for one destination. The question is how long the old one keeps
// signing beside it: long enough to put the new one on the server, and no longer. At once is
// for a secret that has leaked, and every delivery fails until the server has the new one.
const props = defineProps<{
  destination: WebhookDestination
}>()

const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ rolled: [secret: string] }>()

const { rollSecret } = useWebhookActions()

/* Hours the old secret keeps signing: the API takes 0 to 168. */
const OVERLAPS: ChoiceOption[] = [
  { value: '24', label: 'In 24 hours', note: 'Recommended. Both sign for a day: time to deploy the new one with nothing refused.' },
  { value: '1', label: 'In 1 hour', note: 'For a server that picks up a new secret without a deploy.' },
  { value: '168', label: 'In 7 days', note: 'For a server released on a weekly train.' },
  { value: '0', label: 'Now', note: 'The old secret has leaked. Deliveries fail until your server has the new one, then retry.' }
]

const overlap = ref<string | null>(OVERLAPS[0]!.value)
const rolling = ref(false)
const problem = ref('')

async function confirm() {
  rolling.value = true
  problem.value = ''
  try {
    emit('rolled', await rollSecret(props.destination, Number(overlap.value ?? 0)))
    open.value = false
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    rolling.value = false
  }
}
</script>

<template>
  <v-dialog v-model="open" max-width="520">
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          Roll the signing secret
        </h2>
        <p class="m-0 text-xs text-ink/60">
          {{ destination.name }} gets a new secret, signing from now. When should the old one stop?
        </p>
      </header>
      <div class="flex flex-col gap-3 p-5">
        <ChoiceList v-model="overlap" :options="OVERLAPS" label="When the old secret stops" />
        <p v-if="problem" class="m-0 text-xs text-error">
          {{ problem }}
        </p>
      </div>
      <footer class="flex justify-end gap-2 border-t border-line px-5 py-3">
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
          :color="overlap === '0' ? 'error' : 'primary'"
          :loading="rolling"
          class="text-xs font-medium"
          size="small"
          variant="flat"
          @click="confirm"
        >
          Roll secret
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>
