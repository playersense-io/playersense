<script setup lang="ts">
// One event type, the way a developer meets it: what happened, what `data` holds, when it is
// sent from test builds too, and the body exactly as it will be POSTed, in the payload version
// the destination is on.
const props = defineProps<{
  eventType: WebhookEventType
  version: string
}>()

const open = defineModel<boolean>({ required: true })

const code = computed(() => JSON.stringify(eventDocument(props.eventType.type, {
  id: 'evt_7kq2m9x4r8t3zb5n6p1w',
  created: Date.parse('2026-09-24T14:03:11Z'),
  environment: 'production',
  version: props.version,
  seed: props.eventType.type
}), null, 2))

const group = computed(() => WEBHOOK_GROUP[props.eventType.group])
</script>

<template>
  <v-dialog v-model="open" max-width="680" scrollable>
    <v-card border flat>
      <header class="flex items-start gap-3 border-b border-line p-5">
        <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-ink/5 text-lg text-ink/60">
          <Icon :name="group.icon" />
        </span>
        <div class="flex min-w-0 flex-col gap-1">
          <span class="flex items-center gap-2">
            <h2 class="m-0 truncate font-mono text-sm font-medium text-ink">
              {{ eventType.type }}
            </h2>
            <CopyButton :value="eventType.type" label="Copy the event type" />
          </span>
          <span class="text-xs text-ink/60">{{ eventType.summary }}</span>
        </div>
      </header>

      <div class="overflow-y-auto">
        <div class="flex flex-col gap-4 p-5">
          <dl class="m-0 grid gap-3 sm:grid-cols-3">
            <div class="flex flex-col gap-0.5">
              <dt class="text-[11px] text-ink/45">
                Group
              </dt>
              <dd class="m-0 text-xs text-ink">
                {{ group.label }}
              </dd>
            </div>
            <div class="flex flex-col gap-0.5 sm:col-span-2">
              <dt class="text-[11px] text-ink/45">
                data holds
              </dt>
              <dd class="m-0 text-xs text-ink">
                {{ eventType.holds }}
              </dd>
            </div>
            <div class="flex flex-col gap-0.5 sm:col-span-3">
              <dt class="text-[11px] text-ink/45">
                From test builds
              </dt>
              <dd class="m-0 text-xs text-ink">
                {{ eventType.environments ? 'Yes, to a destination that hears staging or development. project.environment says which.' : 'No: it happens in the project, not in a build. Always production.' }}
              </dd>
            </div>
          </dl>
          <CodeBlock :code="code" :title="`${eventType.type} · version ${version}`" />
        </div>
      </div>

      <footer class="flex justify-end border-t border-line px-5 py-3">
        <v-btn
          class="text-xs font-medium"
          color="neutral"
          size="small"
          variant="text"
          @click="open = false"
        >
          Close
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>
