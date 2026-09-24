<script setup lang="ts">
// One flag, and everything about it: who gets what (its rollout, its rules in order, and the
// fallback for everybody else), what production players were actually served, who changed it
// and when, how the game reads it, and the few things about it that are settings rather than
// targeting.
//
// Edits are made to a copy and saved to the draft as one whole-object write, as the API takes
// them (ADR 0034) — checked as they are typed, with each refusal beside the field it is about.
// Saving is not publishing: the strip at the top says, per environment, whether what it serves
// is still what the draft says, and publishing is the step that makes it so.
const route = useRoute()
const router = useRouter()

const { flags, segments, audit, flagByKey, saveFlag, setFlagArchived, historyOf, latest, segmentName } = useLiveConfig()

const key = computed(() => String(route.params.key ?? ''))
const saved = computed(() => flagByKey(key.value))

usePageCrumb(() => saved.value?.name ?? key.value)

const copyOf = (flag: FlagDef | null) => (flag ? JSON.parse(JSON.stringify(flag)) as FlagDef : null)

const draft = shallowRef<FlagDef | null>(copyOf(saved.value))
watch(key, () => {
  draft.value = copyOf(saved.value)
})

/** What an edit can change: the stamps are the server's. */
const editable = (flag: FlagDef | null) => {
  if (!flag) return null
  const { updated_at: _at, updated_by: _by, created_at: _created, ...rest } = flag
  return rest
}

const dirty = computed(() => draft.value !== null && saved.value !== null && !sameJson(editable(draft.value), editable(saved.value)))

/* A value box holding text that does not parse yet, by part: "fallback", "rule-3". */
const unparsed = ref<Record<string, boolean>>({})
const blocked = computed(() => Object.values(unparsed.value).some(Boolean))

const problems = computed(() => (draft.value ? flagProblems(draft.value, segments.value) : []))
const problemsAt = (where: FlagProblem['where']) => problems.value.filter(problem => problem.where === where)

const refusal = ref('')
const savedNotice = ref(false)

function save() {
  if (!draft.value || blocked.value) return
  const problem = saveFlag(draft.value)
  if (problem) {
    refusal.value = problem
    return
  }
  refusal.value = ''
  draft.value = copyOf(flagByKey(key.value))
  savedNotice.value = true
}

function discard() {
  draft.value = copyOf(saved.value)
  unparsed.value = {}
  refusal.value = ''
}

// --- Editing the rules --------------------------------------------------------------------

function patch(next: Partial<FlagDef>) {
  if (draft.value) draft.value = { ...draft.value, ...next }
}

const rules = computed(() => draft.value?.rules ?? [])

function setRule(index: number, rule: FlagVariant) {
  patch({ rules: rules.value.map((entry, at) => (at === index ? rule : entry)) })
}

function addRule() {
  if (!draft.value) return
  const value = draft.value.kind === 'bool' ? !draft.value.fallback : defaultValue(draft.value.kind)
  patch({ rules: [...rules.value, { audience: {}, value: draft.value.kind === 'json' ? JSON.parse(JSON.stringify(draft.value.fallback)) as Json : value, rollout: FULL_ROLLOUT }] })
}

function moveRule(index: number, by: -1 | 1) {
  const next = [...rules.value]
  const [rule] = next.splice(index, 1)
  if (!rule) return
  next.splice(index + by, 0, rule)
  patch({ rules: next })
}

function duplicateRule(index: number) {
  const next = [...rules.value]
  next.splice(index + 1, 0, JSON.parse(JSON.stringify(rules.value[index])) as FlagVariant)
  patch({ rules: next })
}

function removeRule(index: number) {
  patch({ rules: rules.value.filter((_, at) => at !== index) })
  const { [`rule-${index}`]: _gone, ...rest } = unparsed.value
  unparsed.value = rest
}

const fallback = computed({
  get: () => draft.value?.fallback ?? null,
  set: (value: Json) => patch({ fallback: value })
})

const rollout = computed({
  get: () => draft.value?.rollout ?? FULL_ROLLOUT,
  set: (value: number) => patch({ rollout: value })
})

/** The strings this flag serves, offered back in each value box. */
const suggestions = computed(() => (draft.value?.kind === 'string' ? servedValues(documentOf(draft.value)).filter((value): value is string => typeof value === 'string' && value !== '') : []))

// --- Views --------------------------------------------------------------------------------

const TABS: PageTab[] = [
  { value: 'targeting', label: 'Targeting' },
  { value: 'insights', label: 'Exposures' },
  { value: 'history', label: 'History' },
  { value: 'code', label: 'Code' },
  { value: 'settings', label: 'Settings' }
]
const tab = useQueryChoice('tab', TABS.map(entry => entry.value), 'targeting')

/**
 * This flag's own entries, and the publishes and rollbacks that changed what it serves: one that
 * left it as it was is somebody else's news.
 */
const history = computed(() => audit.value.filter((entry) => {
  if (entry.target === key.value && entry.action.startsWith('flag.')) return true
  if ((entry.action !== 'liveops.publish' && entry.action !== 'liveops.rollback') || !entry.environment) return false
  const list = historyOf(entry.environment)
  const at = list.findIndex(version => version.version === entry.version)
  if (at < 0) return false
  const now = list[at]!.flags.find(flag => flag.key === key.value)
  const before = list[at + 1]?.flags.find(flag => flag.key === key.value)
  return !sameJson(now ?? null, before ?? null)
}))

// --- Settings -----------------------------------------------------------------------------

const TAG_OPTIONS = computed(() => [...new Set(flags.value.flatMap(flag => flag.tags))].sort())

const PURPOSE_OPTIONS: ChoiceOption[] = (Object.keys(FLAG_PURPOSES) as FlagPurpose[]).map(value => ({
  value,
  label: FLAG_PURPOSES[value].label,
  note: FLAG_PURPOSES[value].note,
  icon: FLAG_PURPOSES[value].icon
}))

const purpose = computed({
  get: () => draft.value?.purpose ?? null,
  set: (value: string | null) => {
    if (value) patch({ purpose: value as FlagPurpose })
  }
})

const reshuffling = ref(false)

function reshuffle() {
  patch({ salt: Math.random().toString(36).slice(2, 10) })
}

const archiving = ref(false)

// --- Around the page ----------------------------------------------------------------------

const previewing = ref(false)
const publishing = ref(false)
const publishTo = ref<EnvironmentKind>('production')

function openPublish(environment: EnvironmentKind) {
  publishTo.value = environment
  publishing.value = true
}

const duplicating = ref(false)
const creating = computed(() => (duplicating.value ? saved.value : null))

const comparing = ref<EnvironmentKind | null>(null)
const compareOpen = computed({
  get: () => comparing.value !== null,
  set: (value) => {
    if (!value) comparing.value = null
  }
})
const comparison = computed(() => {
  if (!comparing.value || !saved.value) return []
  const published = latest(comparing.value)?.flags.find(flag => flag.key === key.value)
  if (published) return flagChanges(published, documentOf(saved.value), segmentName)
  const count = saved.value.rules.length
  return [`Not in ${ENVIRONMENT_LABELS[comparing.value]} yet. Publishing adds it, serving ${writeValue(saved.value.fallback)}${count ? ` and ${count} ${count === 1 ? 'rule' : 'rules'}` : ''}.`]
})

/* Leaving with edits asks first. Discarding them is what lets the navigation through. */
const leaving = ref<string | null>(null)
const leaveOpen = computed({
  get: () => leaving.value !== null,
  set: (value) => {
    if (!value) leaving.value = null
  }
})

onBeforeRouteLeave((to) => {
  if (!dirty.value) return true
  leaving.value = to.fullPath
  return false
})

/* Another flag is the same page with a new key; a tab is the same page with a new query. */
onBeforeRouteUpdate((to, from) => {
  if (!dirty.value || to.params.key === from.params.key) return true
  leaving.value = to.fullPath
  return false
})

function leave() {
  const to = leaving.value
  leaving.value = null
  discard()
  if (to) void router.push(to)
}

const changeCount = computed(() => {
  if (!draft.value || !saved.value) return 0
  return flagChanges(documentOf(saved.value), documentOf(draft.value)).length
    + (['name', 'description', 'purpose', 'tags', 'grantsValue'] as const).filter(field => !sameJson(draft.value![field], saved.value![field])).length
})
</script>

<template>
  <div v-if="!saved || !draft" class="flex flex-col gap-8">
    <PageHeader title="Flag not found" />
    <LayerCard>
      <EmptyState
        :description="`There is no flag with the key “${key}” in this project. Keys are never reused, so it was never created here.`"
        icon="material-symbols:flag-outline"
        title="No such flag"
      >
        <template #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            to="/growth/flags"
            variant="flat"
          >
            All flags
          </v-btn>
        </template>
      </EmptyState>
    </LayerCard>
  </div>

  <div v-else class="flex flex-col gap-8">
    <PageHeader :title="saved.name">
      <template #mark>
        <span
          aria-hidden="true"
          class="flex size-11 flex-none items-center justify-center rounded-(--ps-radius-large) border border-line bg-surface text-ink/70"
        >
          <Icon :name="FLAG_PURPOSES[saved.purpose].icon" class="text-xl" />
        </span>
      </template>
      <template #badge>
        <StatusBadge v-if="saved.archived" label="Archived" tone="error" />
        <Tag v-else :label="FLAG_KIND_LABELS[saved.kind]" :tone="FLAG_KIND_TONES[saved.kind]" />
      </template>
      <template #description>
        <span class="inline-flex items-center gap-1 align-middle">
          <span class="font-mono text-ink">{{ saved.key }}</span>
          <CopyButton :value="saved.key" label="Copy the key" />
        </span>
        <span v-if="saved.description"> · {{ saved.description }}</span>
      </template>
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="neutral"
          prepend-icon="material-symbols:person-search-outline"
          size="small"
          variant="outlined"
          @click="previewing = true"
        >
          Preview
        </v-btn>
        <v-btn
          :disabled="dirty"
          class="text-xs font-medium"
          color="primary"
          prepend-icon="material-symbols:publish"
          size="small"
          variant="flat"
          @click="openPublish('production')"
        >
          Publish
        </v-btn>
        <v-menu location="bottom end">
          <template #activator="{ props: activator }">
            <v-btn
              v-bind="activator"
              aria-label="More actions"
              color="neutral"
              icon="material-symbols:more-horiz"
              size="small"
              variant="text"
            />
          </template>
          <v-list min-width="200" nav>
            <v-list-item prepend-icon="material-symbols:library-add-outline" title="Duplicate" @click="duplicating = true" />
            <v-list-item
              v-if="saved.archived"
              prepend-icon="material-symbols:unarchive-outline"
              title="Restore"
              @click="setFlagArchived(saved.key, false)"
            />
            <v-list-item
              v-else
              prepend-icon="material-symbols:archive-outline"
              title="Archive"
              @click="archiving = true"
            />
          </v-list>
        </v-menu>
      </template>
    </PageHeader>

    <InfoNote v-if="saved.archived" icon="material-symbols:archive-outline" tone="warning">
      Archived: left out of every publish from here on. Environments keep serving it until their next publish;
      after that the game falls back to the value written in its code. Restore it to put it back.
    </InfoNote>

    <FlagEnvironments :flag="saved" @compare="comparing = $event" />

    <div class="flex flex-col gap-5">
      <PageTabs v-model="tab" :tabs="TABS" label="Flag views" />

      <!-- Targeting: the order the server reads a flag in, top to bottom. -->
      <div v-if="tab === 'targeting'" class="flex flex-col gap-4">
        <SectionCard
          description="Players outside it get the fallback, and no rule is read for them. Which players are in is drawn from their install, so raising it keeps everybody already in."
          title="Who is in the flag"
        >
          <template #badge>
            <span class="font-mono text-xs text-ink/55">{{ formatRollout(rollout) }}</span>
          </template>
          <RolloutField v-model="rollout" label="Flag rollout" />
        </SectionCard>

        <div class="flex flex-col gap-3">
          <div class="flex flex-wrap items-end justify-between gap-2">
            <div class="flex flex-col gap-0.5">
              <h2 class="m-0 text-sm font-medium text-ink">
                Rules
              </h2>
              <p class="m-0 text-xs text-ink/60">
                Read top to bottom. The first rule a player matches answers; the rest are not read.
              </p>
            </div>
            <v-btn
              class="text-xs font-medium"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="tonal"
              @click="addRule"
            >
              Add rule
            </v-btn>
          </div>

          <FlagRuleCard
            v-for="(rule, index) in rules"
            :key="index"
            v-model:invalid="unparsed[`rule-${index}`]"
            :count="rules.length"
            :fallback="draft.fallback"
            :grants-value="draft.grantsValue"
            :index="index"
            :kind="draft.kind"
            :model-value="rule"
            :problems="problemsAt(`rule:${index}`)"
            :segments="segments"
            :suggestions="suggestions"
            @duplicate="duplicateRule(index)"
            @move="moveRule(index, $event)"
            @remove="removeRule(index)"
            @update:model-value="setRule(index, $event)"
          />

          <button
            v-if="!rules.length"
            class="flex flex-col items-center gap-1 rounded-(--ps-radius-large) border border-dashed border-ink/20 px-6 py-8 text-center transition-colors duration-150 hover:border-primary/50 hover:bg-primary/3"
            type="button"
            @click="addRule"
          >
            <Icon aria-hidden="true" class="text-xl text-ink/40" name="material-symbols:alt-route" />
            <span class="text-xs font-medium text-ink">No rules: everybody in the flag gets the fallback</span>
            <span class="text-[11px] text-ink/55">Add a rule to serve something else to a platform, a country, payers, a level range, a segment…</span>
          </button>
        </div>

        <SectionCard
          :description="rules.length ? 'Everybody no rule answered: no match, outside the flag, or outside a matching rule\'s share.' : 'What every player in the flag gets.'"
          title="Everyone else"
        >
          <template #badge>
            <span class="text-xs text-ink/50">fallback</span>
          </template>
          <div class="flex flex-col gap-2">
            <FlagValueEditor
              v-model="fallback"
              v-model:invalid="unparsed.fallback"
              :error="problemsAt('fallback').length > 0"
              :kind="draft.kind"
              :suggestions="suggestions"
              label="Fallback"
            />
            <span v-for="problem in problemsAt('fallback')" :key="problem.message" class="text-[11px] text-error">{{ problem.message }}</span>
          </div>
        </SectionCard>
      </div>

      <FlagInsights v-else-if="tab === 'insights'" :flag="saved" />

      <SectionCard v-else-if="tab === 'history'" description="Edits to this flag, and the publishes that changed what an environment serves for it." title="History">
        <ConfigHistory :entries="history" />
      </SectionCard>

      <SectionCard v-else-if="tab === 'code'" description="How the game reads this flag." title="In the game">
        <FlagCode :flag="saved" />
      </SectionCard>

      <div v-else class="flex flex-col gap-5">
        <!-- A plain form, one field under another at the card's width, each with its label
             above and one line on what it is for. -->
        <SectionCard description="Kept with the flag in the draft. None of it changes what a player receives." title="About this flag">
          <div class="flex flex-col gap-6">
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-medium text-ink" for="flag-name">Name</label>
              <v-text-field
                id="flag-name"
                :model-value="draft.name"
                hide-details
                maxlength="120"
                variant="outlined"
                @update:model-value="patch({ name: $event })"
              />
            </div>

            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-medium text-ink" for="flag-description">Description</label>
              <v-textarea
                id="flag-description"
                :model-value="draft.description"
                auto-grow
                hide-details
                maxlength="500"
                placeholder="What it changes in the game, and who to ask about it"
                rows="4"
                variant="outlined"
                @update:model-value="patch({ description: $event })"
              />
            </div>

            <div class="flex flex-col gap-1.5">
              <span class="text-xs font-medium text-ink">Purpose</span>
              <span class="text-[11px] leading-4 text-ink/55">Decides when the list says it is time to clean up.</span>
              <ChoiceList
                v-model="purpose"
                :columns="2"
                :options="PURPOSE_OPTIONS"
                class="mt-1"
                label="Purpose"
              />
            </div>

            <div class="flex flex-col gap-1.5">
              <span class="text-xs font-medium text-ink">Tags</span>
              <span class="text-[11px] leading-4 text-ink/55">For finding and filtering flags: a team, a system, a release.</span>
              <TagInput
                :model-value="draft.tags"
                :suggestions="TAG_OPTIONS"
                class="mt-1"
                label="Add a tag"
                @update:model-value="patch({ tags: $event })"
              />
            </div>

            <div class="rounded-(--ps-radius-large) border border-line p-4">
              <SwitchRow
                :model-value="draft.grantsValue"
                label="Grants value to players"
                note="Currency, items, discounts or paid content. Rules reading a key the game client sets are then warned about, since a modified client can send any value."
                @update:model-value="patch({ grantsValue: $event })"
              />
            </div>
          </div>
        </SectionCard>

        <SectionCard description="Fixed when the flag was created: the installed game reads the key with a reader of this kind." title="Identity">
          <div class="flex flex-col divide-y divide-line">
            <SettingRow description="What the game reads. A new key is a new flag." fixed="Fixed" label="Key">
              <span class="font-mono text-xs text-ink">{{ saved.key }}</span>
            </SettingRow>
            <SettingRow :description="FLAG_KIND_NOTES[saved.kind]" fixed="Fixed" label="Kind">
              <Tag :label="FLAG_KIND_LABELS[saved.kind]" :tone="FLAG_KIND_TONES[saved.kind]" />
            </SettingRow>
            <SettingRow
              description="Draws every player again for every partial rollout on this flag: in a 50% rollout, about half of those in change sides. For restarting an experiment on fresh players."
              label="Reshuffle players"
            >
              <template #actions>
                <v-btn
                  class="text-xs font-medium"
                  color="neutral"
                  size="small"
                  variant="tonal"
                  @click="reshuffling = true"
                >
                  Reshuffle
                </v-btn>
              </template>
              <template #detail>
                <span class="font-mono text-[11px] text-ink/50">salt: {{ draft.salt || 'none' }}</span>
              </template>
            </SettingRow>
          </div>
        </SectionCard>

        <FlagArchiveCard :flag="saved" @archive="archiving = true" @restore="setFlagArchived(saved.key, false)" />
      </div>
    </div>

    <ActionBar :open="dirty" label="Unsaved flag">
      <template #status>
        <span v-if="refusal" class="text-error">{{ refusal }}</span>
        <span v-else-if="blocked" class="text-error">A value box holds something that is not a value yet</span>
        <span v-else-if="problems.length" class="text-error">{{ problems[0]!.message }}</span>
        <template v-else>
          {{ changeCount }} unsaved {{ changeCount === 1 ? 'change' : 'changes' }} to {{ saved.name }}
        </template>
      </template>
      <v-btn
        class="text-xs font-medium"
        color="neutral"
        size="small"
        variant="text"
        @click="discard"
      >
        Discard
      </v-btn>
      <v-btn
        :disabled="problems.length > 0 || blocked"
        class="text-xs font-medium"
        color="primary"
        size="small"
        variant="flat"
        @click="save"
      >
        Save to draft
      </v-btn>
    </ActionBar>

    <v-snackbar v-model="savedNotice" location="bottom" timeout="4000">
      Saved to the draft. Players get it once it is published.
      <template #actions>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="text"
          @click="savedNotice = false; openPublish('production')"
        >
          Publish…
        </v-btn>
      </template>
    </v-snackbar>

    <!-- Each dialog is mounted only while it is open: one left mounted across the write it
         makes can leave its content painted on the page after it has closed. -->
    <FlagPreviewDialog v-if="previewing" v-model="previewing" :focus="saved.key" />
    <PublishDialog
      v-if="publishing"
      v-model="publishing"
      :environment="publishTo"
      :focus="saved.key"
    />
    <NewFlagDialog
      v-if="duplicating"
      v-model="duplicating"
      :from="creating"
      @created="router.push(`/growth/flags/${encodeURIComponent($event)}`)"
    />

    <v-dialog v-model="compareOpen" max-width="520">
      <v-card border flat>
        <div class="flex flex-col gap-4 p-5">
          <header class="flex flex-col gap-1">
            <h2 class="m-0 text-sm font-medium text-ink">
              {{ comparing ? ENVIRONMENT_LABELS[comparing] : '' }} against the draft
            </h2>
            <p class="m-0 text-xs text-ink/60">
              What publishing the draft there would change for this flag.
            </p>
          </header>
          <ul class="m-0 flex list-none flex-col gap-1.5 p-0">
            <li v-for="line in comparison" :key="line" class="flex items-start gap-2 text-xs text-ink/80">
              <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" name="material-symbols:edit-outline" />
              {{ line }}
            </li>
          </ul>
          <footer class="flex justify-end gap-2">
            <v-btn
              class="text-xs font-medium"
              color="neutral"
              size="small"
              variant="text"
              @click="comparing = null"
            >
              Close
            </v-btn>
            <v-btn
              class="text-xs font-medium"
              color="primary"
              size="small"
              variant="flat"
              @click="comparing && openPublish(comparing); comparing = null"
            >
              Publish…
            </v-btn>
          </footer>
        </div>
      </v-card>
    </v-dialog>

    <ConfirmDialog
      v-if="reshuffling"
      v-model="reshuffling"
      action="Reshuffle"
      description="Every player is drawn again for this flag's partial rollouts once the draft is saved and published. Players already in a rollout may leave it, and others join."
      title="Reshuffle players?"
      tone="primary"
      @confirm="reshuffle"
    />

    <ConfirmDialog
      v-if="archiving"
      v-model="archiving"
      :description="`${saved.name} leaves the draft. Each environment keeps serving it until the next publish there; after that, devices fall back to the value written in the game's code.`"
      :title="`Archive ${saved.name}?`"
      action="Archive flag"
      @confirm="discard(); setFlagArchived(saved.key, true)"
    />

    <ConfirmDialog
      v-if="leaveOpen"
      v-model="leaveOpen"
      :description="`${changeCount} unsaved ${changeCount === 1 ? 'change' : 'changes'} to ${saved.name} will be lost. Nothing has reached the draft or any player.`"
      action="Discard and leave"
      title="Leave without saving?"
      @confirm="leave"
    />
  </div>
</template>
