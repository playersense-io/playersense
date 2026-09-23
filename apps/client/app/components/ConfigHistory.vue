<script setup lang="ts">
// Who changed what, and when: the decisions the audit log records for flags, segments and
// publishes (ADR 0037), newest first, each with the lines it changed. A flag's own page shows
// its entries and the publishes around them, because "when did players start getting this?"
// is answered by the publish, not by the edit.
withDefaults(defineProps<{
  entries: readonly ConfigAuditEntry[]
  /** Name the flag or segment on each row, for a history that mixes them. */
  showTarget?: boolean
}>(), {
  showTarget: false
})

const now = useNow({ interval: 60_000 })

const ACTIONS: Record<AuditAction, { verb: string, icon: string, tone: string }> = {
  'flag.create': { verb: 'created', icon: 'material-symbols:add-circle-outline', tone: 'text-success' },
  'flag.write': { verb: 'edited', icon: 'material-symbols:edit-outline', tone: 'text-ink/60' },
  'flag.archive': { verb: 'archived', icon: 'material-symbols:archive-outline', tone: 'text-error' },
  'flag.restore': { verb: 'restored', icon: 'material-symbols:unarchive-outline', tone: 'text-success' },
  'segment.create': { verb: 'created segment', icon: 'material-symbols:group-add-outline', tone: 'text-success' },
  'segment.write': { verb: 'edited segment', icon: 'material-symbols:edit-outline', tone: 'text-ink/60' },
  'segment.archive': { verb: 'archived segment', icon: 'material-symbols:archive-outline', tone: 'text-error' },
  'segment.restore': { verb: 'restored segment', icon: 'material-symbols:unarchive-outline', tone: 'text-success' },
  'liveops.publish': { verb: 'published to', icon: 'material-symbols:publish', tone: 'text-primary' },
  'draft.restore': { verb: 'restored the draft from', icon: 'material-symbols:history', tone: 'text-warning' }
}
</script>

<template>
  <ol class="m-0 flex list-none flex-col p-0">
    <li v-for="entry in entries" :key="entry.id" class="relative flex gap-3 pb-5 last:pb-0">
      <span aria-hidden="true" class="absolute top-6 bottom-0 left-[11px] w-px bg-line [li:last-child>&]:hidden" />
      <span
        :class="ACTIONS[entry.action].tone"
        class="relative flex size-6 flex-none items-center justify-center rounded-full border border-line bg-surface"
      >
        <Icon :name="ACTIONS[entry.action].icon" aria-hidden="true" class="text-sm" />
      </span>
      <div class="flex min-w-0 flex-1 flex-col gap-1 pt-0.5">
        <p class="m-0 flex flex-wrap items-baseline gap-x-1.5 text-xs text-ink/70">
          <span class="font-medium text-ink">{{ entry.actor }}</span>
          {{ ACTIONS[entry.action].verb }}
          <template v-if="entry.action === 'liveops.publish' || entry.action === 'draft.restore'">
            <span class="font-medium text-ink">{{ ENVIRONMENT_LABELS[entry.environment ?? 'production'] }}</span>
            <span class="font-mono text-[11px] text-ink/50">v{{ entry.version }}</span>
          </template>
          <span v-else-if="showTarget" class="font-mono text-[11px] text-ink">{{ entry.target }}</span>
          <v-tooltip :text="formatDateTime(entry.at)" location="top">
            <template #activator="{ props: tip }">
              <span v-bind="tip" class="ml-auto text-[11px] text-ink/45">{{ sinceWords(entry.at, now) }}</span>
            </template>
          </v-tooltip>
        </p>
        <p v-if="entry.note" class="m-0 text-xs text-ink/80">
          “{{ entry.note }}”
        </p>
        <ul v-if="entry.changes.length && entry.action !== 'liveops.publish'" class="m-0 flex list-none flex-col gap-0.5 p-0">
          <li v-for="line in entry.changes" :key="line" class="text-[11px] leading-4 text-ink/55">
            {{ line }}
          </li>
        </ul>
      </div>
    </li>
    <li v-if="!entries.length" class="py-6 text-center text-xs text-ink/55">
      Nothing recorded yet.
    </li>
  </ol>
</template>
