<script setup lang="ts">
// Where the draft has reached, as three steps in the order a change travels: the team's own
// devices, then testers, then every player. Each step says who sees it, one status (up to date,
// or how many changes behind the draft it is) and, only when it is behind, the one thing to do
// about it. Versions, publishers and notes are the history's to tell; here they were a second
// and third line on every card that nobody could tell apart from the status.
withDefaults(defineProps<{
  /** A strip at the top of a list: no version line under each status. */
  compact?: boolean
}>(), {
  compact: false
})

const emit = defineEmits<{ publish: [environment: EnvironmentKind] }>()

const { flags, segments, latest, draftCheck } = useLiveConfig()

const now = useNow({ interval: 60_000 })

const check = computed(() => draftCheck())

const STEP_ICONS: Record<EnvironmentKind, string> = {
  development: 'material-symbols:code-blocks-outline',
  staging: 'material-symbols:science-outline',
  production: 'material-symbols:public'
}

const stages = computed(() => ENVIRONMENTS.map((environment) => {
  const version = latest(environment)
  const waiting = draftChanges(version, flags.value, segments.value).length
  return { environment, version, waiting }
}))
</script>

<template>
  <ol class="m-0 grid list-none gap-0 overflow-hidden rounded-(--ps-radius-x-large) border border-line bg-surface p-0 md:grid-cols-3">
    <li
      v-for="(stage, index) in stages"
      :key="stage.environment"
      class="relative flex min-w-0 flex-col gap-3 border-line p-4 not-last:border-b md:not-last:border-r md:not-last:border-b-0"
    >
      <!-- The way a change travels, drawn on the line between the steps: centred on it, the line
           being the last pixel of the step before, hence the half pixel. -->
      <Icon
        v-if="index > 0"
        aria-hidden="true"
        class="absolute top-1/2 left-0 hidden -translate-x-[calc(50%+0.5px)] -translate-y-1/2 rounded-full bg-surface text-base text-ink/30 ring-1 ring-line md:block"
        name="material-symbols:chevron-right"
      />

      <div class="flex items-center gap-2.5">
        <span class="flex size-8 flex-none items-center justify-center rounded-(--ps-radius) bg-ink/5 text-base text-ink/60">
          <Icon :name="STEP_ICONS[stage.environment]" aria-hidden="true" />
        </span>
        <span class="flex min-w-0 flex-col">
          <span class="text-sm font-medium text-ink">{{ ENVIRONMENT_LABELS[stage.environment] }}</span>
          <span class="truncate text-[11px] text-ink/50">{{ ENVIRONMENT_WHO[stage.environment] }}</span>
        </span>
      </div>

      <div class="flex items-end justify-between gap-3">
        <div class="flex min-w-0 flex-col gap-0.5">
          <span
            :class="!stage.version ? 'text-ink/50' : stage.waiting ? 'text-warning' : 'text-success'"
            class="flex items-center gap-1 text-xs"
          >
            <Icon
              :name="!stage.version ? 'material-symbols:info-outline' : stage.waiting ? 'material-symbols:schedule-outline' : 'material-symbols:check-circle-outline'"
              aria-hidden="true"
              class="flex-none text-sm"
            />
            <template v-if="!stage.version">Never published</template>
            <template v-else-if="stage.waiting">{{ stage.waiting }} {{ stage.waiting === 1 ? 'change' : 'changes' }} behind</template>
            <template v-else>Up to date</template>
          </span>
          <span v-if="!compact && stage.version" class="truncate text-[11px] text-ink/45">
            v{{ stage.version.version }}, published {{ sinceWords(stage.version.published_at, now) }}
          </span>
        </div>
        <v-btn
          v-if="stage.waiting || !stage.version"
          :color="stage.environment === 'production' ? 'primary' : 'neutral'"
          :disabled="!check.compiles"
          :variant="stage.environment === 'production' ? 'flat' : 'outlined'"
          class="flex-none text-xs font-medium"
          size="small"
          @click="emit('publish', stage.environment)"
        >
          Publish
        </v-btn>
      </div>
    </li>
  </ol>
</template>
