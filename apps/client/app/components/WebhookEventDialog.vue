<script setup lang="ts">
// One event type, the way a developer meets it: what happened, what `data` holds, whether it
// belongs to an environment, and an example body in the envelope every event is POSTed in.
const props = defineProps<{
  eventType: WebhookEventType
}>()

const open = defineModel<boolean>({ required: true })

const code = computed(() => exampleBody(props.eventType.type))

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
            <span v-if="!eventType.emitted" class="rounded-full bg-ink/6 px-1.5 text-[10px] leading-4 font-medium text-ink/60">Not sent yet</span>
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
                Environments
              </dt>
              <dd class="m-0 text-xs text-ink">
                {{ eventType.environments ? 'Belongs to one. A destination hears it only from the environments it listens to, and project.environment says which.' : 'Belongs to the project, not an environment: every destination listening to it hears it, and project.environment is absent.' }}
              </dd>
            </div>
            <div v-if="!eventType.emitted" class="flex flex-col gap-0.5 sm:col-span-3">
              <dt class="text-[11px] text-ink/45">
                Not sent yet
              </dt>
              <dd class="m-0 text-xs text-ink">
                PlayerSense does not send it yet. A destination can listen to it now and hears it once it does.
              </dd>
            </div>
          </dl>
          <CodeBlock :code="code" :title="`An example ${eventType.type} · version ${WEBHOOK_API_VERSION}`" />
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
