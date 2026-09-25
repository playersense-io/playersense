<script setup lang="ts">
// Where one flag stands in each environment: whether the version serving there says what the
// draft says, says something else, or does not have the flag at all. A flag is defined once and
// published per environment, so "it's on in staging but not in production" is a difference
// between two published versions and not two settings — and this strip is where it shows.
const props = defineProps<{
  flag: FlagDef
}>()

const emit = defineEmits<{ compare: [environment: EnvironmentKind] }>()

const { latest, segments } = useLiveConfig()

const now = useNow({ interval: 60_000 })

const STATES: Record<FlagEnvironmentState, { label: string, tone: BadgeTone }> = {
  same: { label: 'As drafted', tone: 'success' },
  changed: { label: 'Draft differs', tone: 'warning' },
  absent: { label: 'Not published', tone: 'neutral' },
  unpublished: { label: 'Never published', tone: 'neutral' }
}

const rows = computed(() => ENVIRONMENTS.map((environment) => {
  const version = latest(environment)
  const state = flagStateIn(props.flag, version, segments.value)
  const published = version?.flags.find(entry => entry.key === props.flag.key) ?? null
  return { environment, version, state, published }
}))
</script>

<template>
  <div class="grid gap-px overflow-hidden rounded-(--ps-radius-large) border border-line bg-line sm:grid-cols-3">
    <div v-for="row in rows" :key="row.environment" class="flex min-w-0 flex-col gap-2 bg-surface p-4">
      <div class="flex items-center justify-between gap-2">
        <span class="text-xs font-medium text-ink">{{ ENVIRONMENT_LABELS[row.environment] }}</span>
        <StatusBadge :label="STATES[row.state].label" :tone="STATES[row.state].tone" />
      </div>
      <div class="flex min-h-5 items-center gap-2 text-xs text-ink/60">
        <template v-if="row.published">
          <span class="text-ink/45">Serves</span>
          <FlagValue :value="row.published.fallback" />
          <span v-if="row.published.rules.length" class="text-ink/45">+ {{ row.published.rules.length }} {{ row.published.rules.length === 1 ? 'rule' : 'rules' }}</span>
        </template>
        <span v-else class="text-ink/45">The game answers with its own default.</span>
      </div>
      <div class="flex items-center justify-between gap-2 text-[11px] text-ink/45">
        <span v-if="row.version" class="truncate">
          <span class="font-mono">v{{ row.version.version }}</span> · {{ sinceWords(row.version.published_at, now) }}
        </span>
        <span v-else>Nothing published yet</span>
        <button
          v-if="row.state === 'changed' || row.state === 'absent'"
          class="flex-none font-medium text-ink/60 underline decoration-ink/25 underline-offset-2 hover:text-ink"
          type="button"
          @click="emit('compare', row.environment)"
        >
          What would change
        </button>
      </div>
    </div>
  </div>
</template>
