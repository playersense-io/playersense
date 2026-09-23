<script setup lang="ts">
// The draft and the three environments, left to right in the order a change travels: what each
// one serves, since when, and how far behind the draft it is. The gap is counted in changes a
// publish there would make, so "Production: 4 waiting" is a sentence a release manager can act
// on — and the button beside it is the act.
withDefaults(defineProps<{
  /** Hide the publishers and notes, for a strip at the top of a list. */
  compact?: boolean
}>(), {
  compact: false
})

const emit = defineEmits<{ publish: [environment: EnvironmentKind] }>()

const { flags, segments, audit, latest, draftCheck } = useLiveConfig()

const now = useNow({ interval: 60_000 })

const check = computed(() => draftCheck())

const lastEdit = computed(() => audit.value.find(entry => entry.action !== 'liveops.publish' && entry.action !== 'draft.restore') ?? null)

const stages = computed(() => ENVIRONMENTS.map((environment) => {
  const version = latest(environment)
  const waiting = draftChanges(version, flags.value, segments.value).length
  return { environment, version, waiting }
}))
</script>

<template>
  <div class="grid gap-2 lg:grid-cols-[minmax(0,0.9fr)_repeat(3,minmax(0,1fr))]">
    <div class="flex min-w-0 flex-col gap-2 rounded-(--ps-radius-large) border border-dashed border-ink/20 bg-canvas p-4">
      <div class="flex items-center justify-between gap-2">
        <span class="flex items-center gap-1.5 text-xs font-medium text-ink">
          <Icon aria-hidden="true" class="text-sm text-ink/50" name="material-symbols:edit-note-outline" />
          Draft
        </span>
        <StatusBadge :label="check.compiles ? 'Compiles' : 'Does not compile'" :tone="check.compiles ? 'success' : 'error'" />
      </div>
      <p class="m-0 text-xs text-ink/60">
        {{ check.flags }} flags · {{ segments.filter(entry => !entry.archived).length }} segments
      </p>
      <p v-if="lastEdit" class="m-0 truncate text-[11px] text-ink/45">
        Edited {{ sinceWords(lastEdit.at, now) }}<template v-if="!compact">
          by {{ lastEdit.actor }}
        </template>
      </p>
      <p v-if="!check.compiles" class="m-0 text-[11px] leading-4 text-error">
        {{ check.problem }}
      </p>
    </div>

    <div
      v-for="stage in stages"
      :key="stage.environment"
      class="relative flex min-w-0 flex-col gap-2 rounded-(--ps-radius-large) border border-line bg-surface p-4"
    >
      <Icon
        aria-hidden="true"
        class="absolute top-1/2 -left-2.5 hidden -translate-y-1/2 rounded-full bg-canvas text-base text-ink/30 lg:block"
        name="material-symbols:chevron-right"
      />
      <div class="flex items-center justify-between gap-2">
        <span class="text-xs font-medium text-ink">{{ ENVIRONMENT_LABELS[stage.environment] }}</span>
        <span v-if="stage.version" class="font-mono text-[11px] text-ink/50">v{{ stage.version.version }}</span>
      </div>
      <p class="m-0 flex items-center gap-1.5 text-xs" :class="stage.waiting ? 'text-ink/80' : 'text-ink/50'">
        <span :class="stage.waiting ? 'bg-warning' : 'bg-success'" aria-hidden="true" class="size-1.5 flex-none rounded-full" />
        {{ stage.waiting ? `${stage.waiting} ${stage.waiting === 1 ? 'change' : 'changes'} waiting` : 'Serves the draft' }}
      </p>
      <p v-if="stage.version" class="m-0 truncate text-[11px] text-ink/45">
        {{ sinceWords(stage.version.published_at, now) }}<template v-if="!compact">
          by {{ stage.version.published_by }}
        </template>
      </p>
      <p v-else class="m-0 text-[11px] text-ink/45">
        Never published
      </p>
      <p v-if="!compact && stage.version?.note" class="m-0 line-clamp-2 text-[11px] leading-4 text-ink/60">
        “{{ stage.version.note }}”
      </p>
      <v-btn
        :disabled="!stage.waiting || !check.compiles"
        class="mt-auto self-start text-xs font-medium"
        :color="stage.environment === 'production' ? 'primary' : 'neutral'"
        size="small"
        :variant="stage.environment === 'production' ? 'flat' : 'tonal'"
        @click="emit('publish', stage.environment)"
      >
        Publish
      </v-btn>
    </div>
  </div>
</template>
