<script setup lang="ts">
// A new signing secret for one destination. The question is how long the old one keeps
// signing beside it: long enough to put the new one on the server, and no longer. At once is
// for a secret that has leaked, and every delivery fails until the server has the new one.
const props = defineProps<{
  destination: WebhookDestination
}>()

const open = defineModel<boolean>({ required: true })

const { rollSecret } = useWebhooks()

const OVERLAPS: ChoiceOption[] = [
  { value: String(24 * 3_600_000), label: 'In 24 hours', note: 'Recommended. Both sign for a day: time to deploy the new one with nothing refused.' },
  { value: String(3_600_000), label: 'In 1 hour', note: 'For a server that picks up a new secret without a deploy.' },
  { value: String(7 * 24 * 3_600_000), label: 'In 7 days', note: 'For a server released on a weekly train.' },
  { value: '0', label: 'Now', note: 'The old secret has leaked. Deliveries fail until your server has the new one, then retry.' }
]

const overlap = ref<string | null>(OVERLAPS[0]!.value)

function confirm() {
  rollSecret(props.destination.id, Number(overlap.value ?? 0))
  open.value = false
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
      <div class="p-5">
        <ChoiceList v-model="overlap" :options="OVERLAPS" label="When the old secret stops" />
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
