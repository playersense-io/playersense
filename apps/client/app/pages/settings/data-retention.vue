<script setup lang="ts">
// Settings > Data retention: how long this project's raw events are kept, what outlives them,
// and the requests that remove one player's data early (§24, §59, §61).
//
// The tier is one of five. How far it can go is the lower of two caps: the plan's
// (max_raw_retention_days) and the collection policy tier's (§98). Lowering it deletes what
// is now too old within a day, and raising it cannot bring back what is gone, so a lower tier
// is asked about first and a higher one says what it does not do.
const { project, privacy, loading, error, reload, setRetention, requestPrivacy, downloadExport } = useProjectSettings()

const policy = computed(() => POLICY_TIERS[project.value?.policy_tier ?? 'standard'])
/** The longest tier this project may have: the lower of the plan's cap and the policy's. */
const cap = computed(() => project.value?.retention_cap ?? 30)
const current = computed<RetentionDays>(() => project.value?.retention_days ?? 30)

const choice = ref<RetentionDays>(30)
watch(current, days => (choice.value = days), { immediate: true })

/** Why a tier cannot be chosen, or null. */
function blocked(days: RetentionDays) {
  if (days <= cap.value) return null
  return days > policy.value.cap ? `Over the ${policy.value.label.toLowerCase()} cap` : 'Needs a larger plan'
}

const lowering = computed(() => choice.value < current.value)
const changed = computed(() => choice.value !== current.value)
const confirming = ref(false)
const saving = ref(false)
const saved = ref(false)
const problem = ref('')

function save() {
  if (lowering.value) {
    confirming.value = true
    return
  }
  void apply()
}

async function apply() {
  saving.value = true
  problem.value = ''
  try {
    await setRetention(choice.value)
    saved.value = true
    setTimeout(() => (saved.value = false), 2400)
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

/* The figure: a year, today on the right, with the raw window against it. */
const share = (days: number) => `${Math.min(100, (days / 365) * 100)}%`

// --- Privacy requests ------------------------------------------------------------------------

const COLUMNS: DataTableColumn<PrivacyRequest>[] = [
  { key: 'requested_at', title: 'Requested', fixed: true, mono: true, width: 170, value: request => Date.parse(request.requested_at) },
  { key: 'kind', title: 'Request', width: 110, skeleton: 'pill' },
  { key: 'player_id', title: 'Player', mono: true, width: 150 },
  { key: 'reference', title: 'Reference', sortable: false },
  { key: 'requested_by', title: 'By', width: 140 },
  { key: 'status', title: 'Status', width: 130, skeleton: 'pill', value: request => PRIVACY_STATUS[request.status]?.label ?? request.status },
  { key: 'completed_at', title: 'Completed', mono: true, width: 170, value: request => (request.completed_at ? Date.parse(request.completed_at) : 0) }
]

const columns = ref(defaultColumnState(COLUMNS))
const sortBy = ref<DataTableSort[]>([{ key: 'requested_at', order: 'desc' }])

const query = ref('')
const filters = ref<Record<string, string[]>>({ kind: [], status: [] })

const FILTER_GROUPS: FilterGroup[] = [
  { key: 'kind', label: 'Request', options: (Object.keys(PRIVACY_KINDS) as PrivacyKind[]).map(kind => ({ value: kind, label: PRIVACY_KINDS[kind].label })) },
  { key: 'status', label: 'Status', options: (Object.keys(PRIVACY_STATUS) as PrivacyStatus[]).map(status => ({ value: status, label: PRIVACY_STATUS[status].label })) }
]

const activeFilters = computed<ActiveFilter[]>(() => [
  ...(filters.value.kind ?? []).map(kind => ({ key: `kind:${kind}`, label: `Request: ${PRIVACY_KINDS[kind as PrivacyKind].label}` })),
  ...(filters.value.status ?? []).map(status => ({ key: `status:${status}`, label: `Status: ${PRIVACY_STATUS[status as PrivacyStatus].label}` })),
  ...(query.value.trim() ? [{ key: 'search', label: `“${query.value.trim()}”` }] : [])
])

function removeFilter(key: string) {
  if (key === 'search') {
    query.value = ''
    return
  }
  const [group, value] = key.split(':') as [string, string]
  filters.value = { ...filters.value, [group]: (filters.value[group] ?? []).filter(entry => entry !== value) }
}

function clearFilters() {
  query.value = ''
  filters.value = { kind: [], status: [] }
}

const matched = computed(() => {
  const needle = query.value.trim().toLowerCase()
  const kinds = filters.value.kind ?? []
  const statuses = filters.value.status ?? []
  return privacy.value.filter(request => (!kinds.length || kinds.includes(request.kind))
    && (!statuses.length || statuses.includes(request.status))
    && (!needle || request.player_id.toLowerCase().includes(needle) || (request.reference ?? '').toLowerCase().includes(needle)))
})

/* A failed request is worth retrying only until a later one for the same player has run. */
const retryable = (request: PrivacyRequest) => request.status === 'failed'
  && !privacy.value.some(other => other.kind === request.kind && other.player_id === request.player_id && other.status !== 'failed' && other.requested_at > request.requested_at)

// --- A new request ---------------------------------------------------------------------------

const KINDS: SegmentedOption[] = [
  { value: 'erase', label: 'Erase a player' },
  { value: 'export', label: 'Export a player' }
]

const asking = ref(false)
const kind = ref<PrivacyKind>('erase')
const playerId = ref('')
const reference = ref('')
const ids = { player: useId(), reference: useId() }

function ask(next: PrivacyKind = 'erase', player = '') {
  kind.value = next
  playerId.value = player
  reference.value = ''
  asking.value = true
}

const submitting = ref(false)
const askProblem = ref('')

watch(asking, open => open && (askProblem.value = ''))

async function submit() {
  if (!playerId.value.trim()) return
  submitting.value = true
  askProblem.value = ''
  try {
    await requestPrivacy(kind.value, playerId.value, reference.value)
    asking.value = false
  } catch (caught) {
    askProblem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    submitting.value = false
  }
}

async function download(request: PrivacyRequest) {
  problem.value = ''
  try {
    await downloadExport(request)
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  }
}
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader description="How long this project's raw events are kept, what outlives them, and the requests that remove one player's data sooner." title="Data retention" />

    <LoadState
      :error="error"
      :loading="loading"
      what="the project"
      @retry="reload"
    >
      <template v-if="project">
        <p v-if="problem" class="m-0 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs text-error ring-1 ring-error/20 ring-inset">
          {{ problem }}
        </p>

        <SectionCard description="Events as they were sent, with the player they came from. They are what replays, exports and player views read, and they are what storage is billed on." title="Raw events">
          <div class="flex flex-col gap-5">
            <div class="grid grid-cols-5 gap-2" role="radiogroup" aria-label="Keep raw events for">
              <button
                v-for="days in RETENTION_TIERS"
                :key="days"
                :aria-checked="choice === days"
                :class="[
                  choice === days ? 'border-primary bg-primary/6 ring-1 ring-primary ring-inset' : 'border-line',
                  blocked(days) ? 'cursor-not-allowed opacity-50' : 'cursor-pointer hover:bg-ink/3'
                ]"
                :disabled="Boolean(blocked(days))"
                class="flex flex-col items-start gap-0.5 rounded-(--ps-radius) border px-3 py-2.5 text-left transition-colors duration-150"
                role="radio"
                type="button"
                @click="choice = days"
              >
                <span class="font-mono text-base text-ink">{{ days }}</span>
                <span class="text-[11px] text-ink/55">{{ blocked(days) ?? (days === project.retention_days ? 'Current' : 'days') }}</span>
              </button>
            </div>

            <!-- A year, today on the right: the raw window against the aggregates that outlive it. -->
            <figure class="m-0 flex flex-col gap-2.5" aria-label="What is kept, over a year">
              <div class="flex items-center gap-3">
                <span class="w-24 flex-none text-[11px] text-ink/60">Raw events</span>
                <span class="relative h-2.5 flex-1 overflow-hidden rounded-full bg-ink/6">
                  <span :style="{ width: share(choice) }" class="absolute inset-y-0 right-0 rounded-full bg-primary transition-[width] duration-300" />
                  <span v-if="lowering" :style="{ right: share(choice), width: share(project.retention_days - choice), background: 'repeating-linear-gradient(135deg, rgba(var(--v-theme-error), 0.55) 0 3px, transparent 3px 6px)' }" class="absolute inset-y-0" />
                </span>
                <span class="w-20 flex-none text-right font-mono text-[11px] text-ink/70">{{ choice }} days</span>
              </div>
              <div class="flex items-center gap-3">
                <span class="w-24 flex-none text-[11px] text-ink/60">Aggregates</span>
                <span class="h-2.5 flex-1 rounded-full bg-success/70" />
                <span class="w-20 flex-none text-right text-[11px] text-ink/70">Always</span>
              </div>
              <figcaption class="flex justify-between pr-23 pl-27 text-[10px] text-ink/45">
                <span>A year ago</span>
                <span>Today</span>
              </figcaption>
            </figure>

            <p v-if="lowering" class="m-0 flex items-start gap-2.5 rounded-(--ps-radius) bg-error/6 px-3.5 py-3 text-xs leading-5 text-ink/80 ring-1 ring-error/20 ring-inset">
              <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-error" name="material-symbols:warning-outline" />
              Raw events older than {{ choice }} days are deleted within a day of saving. They cannot be brought back by raising the tier again.
            </p>
            <p v-else-if="changed" class="m-0 flex items-start gap-2.5 rounded-(--ps-radius) bg-ink/4 px-3.5 py-3 text-xs leading-5 text-ink/75">
              <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-primary" name="material-symbols:info-outline" />
              Events are kept for {{ choice }} days from now on. What was already deleted under {{ project.retention_days }} days stays deleted, so the full {{ choice }} days fill in over time.
            </p>

            <div class="flex flex-wrap items-center justify-end gap-2 border-t border-line pt-4">
              <span class="mr-auto text-[11px] text-ink/50">
                Up to {{ cap }} days for this project<template v-if="policy.cap === cap">, under {{ policy.label.toLowerCase() }} collection</template>.
                <span v-if="project.retention_changed_at">Last changed {{ formatDate(project.retention_changed_at) }}.</span>
                <span v-if="project.retention_purge_until && Date.parse(project.retention_purge_until) > Date.now()">Older events are being deleted until {{ formatDate(project.retention_purge_until) }}.</span>
              </span>
              <span v-if="saved" class="flex items-center gap-1 text-xs text-success">
                <Icon aria-hidden="true" class="text-sm" name="material-symbols:check" />
                Saved
              </span>
              <v-btn
                :disabled="!changed"
                class="text-xs font-medium"
                color="neutral"
                size="small"
                variant="text"
                @click="choice = project.retention_days"
              >
                Discard
              </v-btn>
              <v-btn
                :color="lowering ? 'error' : 'primary'"
                :disabled="!changed"
                :loading="saving"
                class="text-xs font-medium"
                size="small"
                variant="flat"
                @click="save"
              >
                {{ lowering ? `Lower to ${choice} days` : 'Save' }}
              </v-btn>
            </div>
          </div>
        </SectionCard>

        <SectionCard description="Two kinds of data with two lifetimes. A chart built from counts outlives the events it was counted from." title="What is kept, and for how long">
          <div class="grid gap-4 md:grid-cols-2">
            <section class="flex flex-col gap-3 rounded-(--ps-radius-large) border border-line p-4">
              <header class="flex items-center justify-between gap-2">
                <h3 class="m-0 flex items-center gap-2 text-sm font-medium text-ink">
                  <span aria-hidden="true" class="size-2 rounded-full bg-primary" />
                  Raw, player by player
                </h3>
                <span class="font-mono text-xs text-ink/65">{{ project.retention_days }} days</span>
              </header>
              <ul class="m-0 flex list-none flex-col gap-1.5 p-0 text-xs text-ink/75">
                <li>Events and their properties</li>
                <li>Sessions</li>
                <li>Each player's profile and lifetime figures</li>
                <li>Each player's daily activity</li>
              </ul>
              <p class="m-0 border-t border-line pt-3 text-[11px] leading-4 text-ink/55">
                Deleted as it ages past the tier, or at once when the player is erased.
              </p>
            </section>
            <section class="flex flex-col gap-3 rounded-(--ps-radius-large) border border-line p-4">
              <header class="flex items-center justify-between gap-2">
                <h3 class="m-0 flex items-center gap-2 text-sm font-medium text-ink">
                  <span aria-hidden="true" class="size-2 rounded-full bg-success" />
                  Aggregates, counts only
                </h3>
                <span class="text-xs text-ink/65">For the life of the project</span>
              </header>
              <ul class="m-0 flex list-none flex-col gap-1.5 p-0 text-xs text-ink/75">
                <li>Daily players, sessions, playtime and revenue</li>
                <li>Retention and cohort curves</li>
                <li>Funnel and experiment results</li>
              </ul>
              <p class="m-0 border-t border-line pt-3 text-[11px] leading-4 text-ink/55">
                They hold counts, not players, so a 365-day retention curve is still there on a {{ project.retention_days }}-day tier.
              </p>
            </section>
          </div>
        </SectionCard>

        <SectionCard :padded="false" description="A player's right to have their data erased or handed to them. An erased player is hidden at once, and their rows are gone everywhere within 30 days." title="Privacy requests">
          <template #actions>
            <v-btn
              class="text-xs font-medium"
              color="primary"
              prepend-icon="material-symbols:add"
              size="small"
              variant="flat"
              @click="ask()"
            >
              New request
            </v-btn>
          </template>

          <TableToolbar :filters="activeFilters" @clear="clearFilters" @remove="removeFilter">
            <SearchField
              v-model="query"
              class="w-56"
              label="Search requests"
              placeholder="Player or reference"
            />
            <FilterMenu v-model="filters" :groups="FILTER_GROUPS" />
            <DataTableColumnsMenu v-model="columns" :columns="COLUMNS" />
          </TableToolbar>

          <DataTable
            v-model:column-state="columns"
            v-model:sort-by="sortBy"
            :columns="COLUMNS"
            :items="matched"
            empty-description="No request matches these filters."
            empty-title="Nothing matches."
            label="Privacy requests"
          >
            <template #cell.requested_at="{ item }">
              {{ formatDateTime(item.requested_at) }}
            </template>
            <template #cell.kind="{ item }">
              <Tag :label="PRIVACY_KINDS[item.kind].label" :tone="PRIVACY_KINDS[item.kind].tone" />
            </template>
            <template #cell.player_id="{ item }">
              <span class="text-ink">{{ item.player_id }}</span>
            </template>
            <template #cell.reference="{ item }">
              <span class="text-ink/70">{{ item.reference ?? '' }}</span>
            </template>
            <template #cell.status="{ item }">
              <StatusBadge :label="PRIVACY_STATUS[item.status]?.label ?? item.status" :tone="PRIVACY_STATUS[item.status]?.tone ?? 'neutral'" />
              <v-tooltip
                v-if="item.failure_reason"
                activator="parent"
                location="top"
                :text="item.failure_reason"
              />
            </template>
            <template #cell.completed_at="{ item }">
              <span class="text-ink/65">{{ item.completed_at ? formatDateTime(item.completed_at) : '' }}</span>
            </template>

            <template #actions="{ item }">
              <v-btn
                v-if="retryable(item)"
                :aria-label="`Try the ${item.kind} of ${item.player_id} again`"
                icon
                size="small"
                variant="text"
                @click="ask(item.kind, item.player_id)"
              >
                <Icon aria-hidden="true" class="text-lg" name="material-symbols:refresh" />
                <v-tooltip activator="parent" location="top" text="Try again" />
              </v-btn>
              <v-btn
                v-else-if="item.kind === 'export' && item.status === 'completed' && item.download_path"
                :aria-label="`Download the export of ${item.player_id}`"
                icon
                size="small"
                variant="text"
                @click="download(item)"
              >
                <Icon aria-hidden="true" class="text-lg" name="material-symbols:download" />
                <v-tooltip activator="parent" location="top" text="Download, JSON" />
              </v-btn>
            </template>
          </DataTable>
        </SectionCard>

        <ConfirmDialog
          v-model="confirming"
          :action="`Lower to ${choice} days`"
          :description="`Raw events older than ${choice} days are deleted within a day, and raising the tier again will not bring them back. Aggregates are kept.`"
          :title="`Keep raw events for ${choice} days?`"
          @confirm="apply"
        />
      </template>
    </LoadState>

    <v-dialog v-model="asking" max-width="480">
      <v-card border flat>
        <form class="flex flex-col gap-5 p-5" @submit.prevent="submit">
          <header class="flex flex-col gap-1">
            <h2 class="m-0 text-sm font-medium text-ink">
              New privacy request
            </h2>
            <p class="m-0 text-xs leading-5 text-ink/60">
              For a player who asked for their data, or asked for it to be gone. Use the player ID your game sends.
            </p>
          </header>

          <SegmentedControl
            v-model="kind"
            :options="KINDS"
            label="Request"
            stretch
          />

          <div class="flex flex-col gap-1.5">
            <label :for="ids.player" class="text-xs text-ink/60">Player ID</label>
            <v-text-field
              :id="ids.player"
              v-model="playerId"
              autocomplete="off"
              autofocus
              class="font-mono"
              density="comfortable"
              hide-details
              placeholder="p_8f2a61c0"
              variant="outlined"
            />
          </div>
          <div class="flex flex-col gap-1.5">
            <label :for="ids.reference" class="text-xs text-ink/60">Reference <span class="text-ink/40">(optional)</span></label>
            <v-text-field
              :id="ids.reference"
              v-model="reference"
              autocomplete="off"
              density="comfortable"
              hide-details
              maxlength="80"
              placeholder="Support ticket, form, email"
              variant="outlined"
            />
          </div>

          <p class="m-0 flex items-start gap-2 rounded-(--ps-radius-small) bg-ink/4 px-3 py-2.5 text-[11px] leading-4 text-ink/70">
            <Icon aria-hidden="true" class="mt-px flex-none text-sm text-ink/45" name="material-symbols:info-outline" />
            <template v-if="kind === 'erase'">
              The player is hidden from every view at once. Their events, sessions and profile are deleted everywhere within 30 days, and cannot be restored. Counts they were part of stay.
            </template>
            <template v-else>
              Everything held about the player, as one JSON file, usually within a day. It is listed here to download, and the link works for 7 days.
            </template>
          </p>

          <p v-if="askProblem" class="m-0 text-xs text-error">
            {{ askProblem }}
          </p>

          <footer class="flex items-center justify-end gap-2">
            <v-btn
              class="text-xs font-medium"
              color="neutral"
              size="small"
              variant="text"
              @click="asking = false"
            >
              Cancel
            </v-btn>
            <v-btn
              :color="kind === 'erase' ? 'error' : 'primary'"
              :disabled="!playerId.trim()"
              :loading="submitting"
              class="text-xs font-medium"
              size="small"
              type="submit"
              variant="flat"
            >
              {{ kind === 'erase' ? 'Erase player' : 'Export player' }}
            </v-btn>
          </footer>
        </form>
      </v-card>
    </v-dialog>
  </div>
</template>
