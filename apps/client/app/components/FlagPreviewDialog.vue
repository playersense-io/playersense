<script setup lang="ts">
// What one player would be served, flag by flag, and why: which rule answered, or which rollout
// left them out and at what bucket. The same evaluation the server runs on /v1/config — the
// first matching rule, a rule's rollout answering with the fallback rather than falling
// through, SHA-256 buckets keyed by the install — against the draft or against whatever an
// environment serves now.
//
// The player is described the way the server sees one: what the SDK sends, what the connection
// says, what the player's history holds, and the game's own context. A player without a player
// id has no `.player.` history, as on the server.
const props = withDefaults(defineProps<{
  /** A flag to put first. */
  focus?: string
}>(), {
  focus: ''
})

const open = defineModel<boolean>({ required: true })

const { flags, segments, latest, segmentName } = useLiveConfig()
const { project } = useWorkspace()

type Source = 'draft' | EnvironmentKind

const source = ref<Source>('draft')
const SOURCES = computed<SegmentedOption[]>(() => [
  { value: 'draft', label: 'Draft' },
  ...ENVIRONMENTS.map(environment => ({ value: environment, label: ENVIRONMENT_LABELS[environment], disabled: !latest(environment) }))
])

const newInstall = () => crypto.randomUUID()

const install = ref(newInstall())
const player = ref('')
const platform = ref<string>('android')
const appVersion = ref('2.5.1')
const country = ref<string>('TR')
const locale = ref('tr-TR')
const vpn = ref(false)
const spend = ref('0')
const purchases = ref('0')
const daysSinceInstall = ref('1')
const daysSinceSeen = ref('0')
const sessions = ref('3')
const context = ref<{ key: string, value: string }[]>([
  { key: 'progress.level', value: '4' },
  { key: 'rank', value: 'bronze' },
  { key: 'tutorial.completed', value: 'false' },
  { key: 'quality', value: 'medium' }
])

interface Persona {
  label: string
  spend: string
  purchases: string
  daysSinceInstall: string
  daysSinceSeen: string
  sessions: string
  level: string
  player: string
}

/** Players worth checking, one press away. */
const PERSONAS: Persona[] = [
  { label: 'New player', spend: '0', purchases: '0', daysSinceInstall: '1', daysSinceSeen: '0', sessions: '3', level: '4', player: '' },
  { label: 'Whale', spend: '240', purchases: '31', daysSinceInstall: '210', daysSinceSeen: '0', sessions: '640', level: '87', player: 'player_1842' },
  { label: 'Lapsed payer', spend: '9.99', purchases: '1', daysSinceInstall: '120', daysSinceSeen: '21', sessions: '48', level: '23', player: 'player_0977' }
]

function apply(persona: Persona) {
  spend.value = persona.spend
  purchases.value = persona.purchases
  daysSinceInstall.value = persona.daysSinceInstall
  daysSinceSeen.value = persona.daysSinceSeen
  sessions.value = persona.sessions
  player.value = persona.player
  const level = context.value.find(entry => entry.key === 'progress.level')
  if (level) level.value = persona.level
  else context.value.push({ key: 'progress.level', value: persona.level })
}

const CONTINENTS: Record<string, string> = {
  US: 'NA', CA: 'NA', MX: 'NA', BR: 'SA', AR: 'SA', CO: 'SA', GB: 'EU', DE: 'EU', FR: 'EU', IT: 'EU', ES: 'EU', NL: 'EU', PL: 'EU', RU: 'EU', TR: 'AS',
  JP: 'AS', KR: 'AS', CN: 'AS', IN: 'AS', ID: 'AS', PH: 'AS', VN: 'AS', TH: 'AS', MY: 'AS', SA: 'AS', AE: 'AS', AU: 'OC', EG: 'AF', NG: 'AF', ZA: 'AF'
}

/** A value typed into the context list, as the SDK would send it: a number, a bool, or text. */
function scalar(text: string): Scalar {
  const trimmed = text.trim()
  if (trimmed === 'true' || trimmed === 'false') return trimmed === 'true'
  if (trimmed !== '' && Number.isFinite(Number(trimmed))) return Number(trimmed)
  return trimmed
}

const facts = computed<TargetFacts>(() => {
  const number = (text: string) => Math.max(0, Number(text) || 0)
  const out: TargetFacts = {
    'app.version': appVersion.value,
    'device.platform': platform.value,
    'device.locale': locale.value,
    'sdk.name': 'unity',
    'sdk.version': '0.1.0',
    'network.vpn': vpn.value,
    'network.proxy': false,
    'network.tor': false,
    'network.relay': false,
    'network.hosting': false
  }
  if (country.value) {
    out['geo.country'] = country.value.toUpperCase()
    const continent = CONTINENTS[country.value.toUpperCase()]
    if (continent) out['geo.continent'] = continent
  }
  const history = (scope: 'install' | 'player') => {
    const bought = number(purchases.value)
    out[`commerce.${scope}.lifetime_spend_micros`] = Math.round(number(spend.value) * 1_000_000)
    out[`commerce.${scope}.purchases`] = bought
    if (bought > 0) {
      out[`commerce.${scope}.days_since_first_purchase`] = Math.round(number(daysSinceInstall.value) * 0.6)
      out[`commerce.${scope}.days_since_last_purchase`] = number(daysSinceSeen.value) + 2
    }
    out[`activity.${scope}.days_since_install`] = number(daysSinceInstall.value)
    out[`activity.${scope}.days_since_last_seen`] = number(daysSinceSeen.value)
    out[`activity.${scope}.sessions`] = number(sessions.value)
    out[`activity.${scope}.days_active`] = Math.min(number(daysSinceInstall.value) + 1, number(sessions.value))
    out[`activity.${scope}.playtime_seconds`] = number(sessions.value) * 640
  }
  history('install')
  if (player.value.trim()) history('player')
  for (const entry of context.value) {
    const key = entry.key.trim().replace(/^context\./, '')
    if (key && entry.value.trim() !== '') out[`context.${key}`] = scalar(entry.value)
  }
  return out
})

/** The document being asked: the draft as it stands, or an environment's latest version. */
const documents = computed(() => {
  if (source.value === 'draft') {
    return {
      flags: flags.value.filter(flag => !flag.archived).map(documentOf),
      segments: segments.value.filter(entry => !entry.archived).map(entry => ({ key: entry.key, rule: entry.rule }))
    }
  }
  const version = latest(source.value)
  return { flags: version?.flags ?? [], segments: version?.segments ?? [] }
})

interface Row {
  flag: FlagDocument
  name: string
  answer: Answer
}

const rows = shallowRef<Row[]>([])
let asked = 0

watch([documents, facts, install, open], async () => {
  if (!open.value) return
  const ticket = ++asked
  const rules = new Map(documents.value.segments.map(entry => [entry.key, entry.rule]))
  const answers = await Promise.all(documents.value.flags.map(async flag => ({
    flag,
    name: flags.value.find(entry => entry.key === flag.key)?.name ?? flag.key,
    answer: await answerFor(project.value.id, flag, facts.value, install.value, rules)
  })))
  if (ticket !== asked) return
  rows.value = answers.sort((a, b) => Number(b.flag.key === props.focus) - Number(a.flag.key === props.focus) || a.flag.key.localeCompare(b.flag.key))
}, { immediate: true, deep: true })

function why(row: Row): string {
  const { resolution } = row.answer
  const ruleName = (index: number) => `rule ${index + 1} (${describeRule(row.flag.rules[index]?.audience, segmentName)})`
  switch (resolution.kind) {
    case 'rule':
      return resolution.bucket === null
        ? `Matched ${ruleName(resolution.rule)}`
        : `Matched ${ruleName(resolution.rule)}, and inside its ${formatRollout(row.flag.rules[resolution.rule]!.rollout)} at bucket ${resolution.bucket.toLocaleString('en')}`
    case 'rule-rollout':
      return `Matched ${ruleName(resolution.rule)}, but outside its ${formatRollout(row.flag.rules[resolution.rule]!.rollout)} at bucket ${resolution.bucket.toLocaleString('en')}: the fallback`
    case 'flag-rollout':
      return `Outside the flag's ${formatRollout(row.flag.rollout)} at bucket ${resolution.bucket.toLocaleString('en')}: the fallback`
    case 'missing-install':
      return 'No install id, so no partial rollout can include them: the fallback'
    default:
      return row.flag.rules.length ? 'No rule matched: the fallback' : 'No rules: the fallback'
  }
}

const TONES: Record<Resolution['kind'], string> = {
  'rule': 'bg-success',
  'rule-rollout': 'bg-warning',
  'flag-rollout': 'bg-warning',
  'missing-install': 'bg-warning',
  'no-rule': 'bg-ink/30'
}

/** The snapshot the SDK would receive: values only, never a rule (ADR 0027). */
const snapshot = computed(() => JSON.stringify({
  version: source.value === 'draft' ? null : latest(source.value)?.version ?? 0,
  flags: Object.fromEntries(rows.value.map(row => [row.flag.key, row.answer.value as unknown]))
}, null, 2))

const showSnapshot = ref(false)

const PLATFORM_ITEMS = TARGET_PLATFORMS.map(value => ({ value, title: value }))
const COUNTRY_ITEMS = COUNTRY_SUGGESTIONS.map(code => ({ value: code, title: `${code} · ${countryName(code)}` }))
</script>

<template>
  <v-dialog v-model="open" max-width="1080" scrollable>
    <v-card border flat>
      <header class="flex flex-wrap items-start justify-between gap-3 border-b border-line p-5">
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            Preview a player
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            Describe a player and see what every flag serves them, evaluated as the server evaluates it.
          </p>
        </div>
        <SegmentedControl v-model="source" :options="SOURCES" label="Evaluate against" />
      </header>

      <div class="grid min-h-0 overflow-y-auto lg:grid-cols-[20rem_minmax(0,1fr)]">
        <aside class="flex flex-col gap-5 border-b border-line p-5 lg:border-r lg:border-b-0">
          <div class="flex flex-wrap gap-1">
            <v-btn
              v-for="persona in PERSONAS"
              :key="persona.label"
              class="text-xs"
              color="neutral"
              size="small"
              variant="tonal"
              @click="apply(persona)"
            >
              {{ persona.label }}
            </v-btn>
          </div>

          <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
            <legend class="mb-1 flex items-center gap-1.5 p-0 text-[11px] text-ink/50">
              <AuthorityMark authority="sdk" /> Sent by the SDK
            </legend>
            <div class="flex items-end gap-1">
              <div class="flex min-w-0 flex-1 flex-col gap-1">
                <span class="text-[11px] text-ink/55">Install id</span><v-text-field
                  v-model="install"
                  class="ps-preview-field font-mono"
                  density="compact"
                  hide-details
                  aria-label="Install id"
                  variant="outlined"
                />
              </div>
              <v-btn
                aria-label="New install id"
                color="neutral"
                icon="material-symbols:refresh"
                size="small"
                variant="text"
                @click="install = newInstall()"
              />
            </div>
            <div class="flex min-w-0 flex-col gap-1">
              <span class="text-[11px] text-ink/55">Player id (signed in)</span><v-text-field
                v-model="player"
                class="ps-preview-field"
                density="compact"
                hide-details
                aria-label="Player id (signed in)"
                placeholder="None"
                variant="outlined"
              />
            </div>
            <div class="grid grid-cols-2 gap-2">
              <div class="flex min-w-0 flex-col gap-1">
                <span class="text-[11px] text-ink/55">Platform</span><v-select
                  v-model="platform"
                  :items="PLATFORM_ITEMS"
                  class="ps-preview-field"
                  density="compact"
                  hide-details
                  aria-label="Platform"
                  variant="outlined"
                />
              </div>
              <div class="flex min-w-0 flex-col gap-1">
                <span class="text-[11px] text-ink/55">App version</span><v-text-field
                  v-model="appVersion"
                  class="ps-preview-field"
                  density="compact"
                  hide-details
                  aria-label="App version"
                  variant="outlined"
                />
              </div>
            </div>
            <div class="flex min-w-0 flex-col gap-1">
              <span class="text-[11px] text-ink/55">Locale</span><v-text-field
                v-model="locale"
                class="ps-preview-field"
                density="compact"
                hide-details
                aria-label="Locale"
                variant="outlined"
              />
            </div>
          </fieldset>

          <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
            <legend class="mb-1 flex items-center gap-1.5 p-0 text-[11px] text-ink/50">
              <AuthorityMark authority="connection" /> From the connection
            </legend>
            <div class="flex min-w-0 flex-col gap-1">
              <span class="text-[11px] text-ink/55">Country</span><v-combobox
                v-model="country"
                :items="COUNTRY_ITEMS"
                :return-object="false"
                class="ps-preview-field"
                density="compact"
                hide-details
                aria-label="Country"
                variant="outlined"
              />
            </div>
            <v-switch
              v-model="vpn"
              class="ps-preview-switch"
              color="primary"
              density="compact"
              hide-details
              inset
              label="On a VPN"
            />
          </fieldset>

          <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
            <legend class="mb-1 flex items-center gap-1.5 p-0 text-[11px] text-ink/50">
              <AuthorityMark authority="history" /> From their history
            </legend>
            <div class="grid grid-cols-2 gap-2">
              <div class="flex min-w-0 flex-col gap-1">
                <span class="text-[11px] text-ink/55">Lifetime spend</span><v-text-field
                  v-model="spend"
                  class="ps-preview-field"
                  density="compact"
                  hide-details
                  aria-label="Lifetime spend"
                  prefix="$"
                  variant="outlined"
                />
              </div>
              <div class="flex min-w-0 flex-col gap-1">
                <span class="text-[11px] text-ink/55">Purchases</span><v-text-field
                  v-model="purchases"
                  class="ps-preview-field"
                  density="compact"
                  hide-details
                  aria-label="Purchases"
                  variant="outlined"
                />
              </div>
              <div class="flex min-w-0 flex-col gap-1">
                <span class="text-[11px] text-ink/55">Days since install</span><v-text-field
                  v-model="daysSinceInstall"
                  class="ps-preview-field"
                  density="compact"
                  hide-details
                  aria-label="Days since install"
                  variant="outlined"
                />
              </div>
              <div class="flex min-w-0 flex-col gap-1">
                <span class="text-[11px] text-ink/55">Days away</span><v-text-field
                  v-model="daysSinceSeen"
                  class="ps-preview-field"
                  density="compact"
                  hide-details
                  aria-label="Days away"
                  variant="outlined"
                />
              </div>
              <div class="flex min-w-0 flex-col gap-1">
                <span class="text-[11px] text-ink/55">Sessions</span><v-text-field
                  v-model="sessions"
                  class="ps-preview-field"
                  density="compact"
                  hide-details
                  aria-label="Sessions"
                  variant="outlined"
                />
              </div>
            </div>
          </fieldset>

          <fieldset class="m-0 flex flex-col gap-2 border-0 p-0">
            <legend class="mb-1 flex items-center gap-1.5 p-0 text-[11px] text-ink/50">
              <AuthorityMark authority="client" /> Set by the game
            </legend>
            <div v-for="(entry, index) in context" :key="index" class="flex items-center gap-1.5">
              <v-text-field
                v-model="entry.key"
                class="ps-preview-field font-mono"
                density="compact"
                hide-details
                placeholder="key"
                prefix="context."
                variant="outlined"
              />
              <v-text-field
                v-model="entry.value"
                class="ps-preview-field w-24 flex-none"
                density="compact"
                hide-details
                placeholder="value"
                variant="outlined"
              />
              <v-btn
                aria-label="Remove"
                color="neutral"
                icon="material-symbols:close"
                size="x-small"
                variant="text"
                @click="context.splice(index, 1)"
              />
            </div>
            <v-btn
              class="self-start text-xs"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="text"
              @click="context.push({ key: '', value: '' })"
            >
              Context key
            </v-btn>
          </fieldset>
        </aside>

        <section class="flex min-w-0 flex-col">
          <div class="flex items-center justify-between gap-2 border-b border-line px-5 py-3">
            <span class="text-xs text-ink/60">{{ rows.length }} flags {{ source === 'draft' ? 'in the draft' : `in ${ENVIRONMENT_LABELS[source]}` }}</span>
            <v-btn
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:data-object"
              size="small"
              variant="text"
              @click="showSnapshot = !showSnapshot"
            >
              {{ showSnapshot ? 'Show reasons' : 'Show the snapshot' }}
            </v-btn>
          </div>

          <div v-if="showSnapshot" class="flex flex-col gap-2 p-5">
            <p class="m-0 text-xs text-ink/60">
              What the SDK receives: values, never the rules that chose them.
            </p>
            <CodeBlock :code="snapshot" class="max-h-[60vh]" />
          </div>

          <ul v-else class="m-0 list-none p-0">
            <li
              v-for="row in rows"
              :key="row.flag.key"
              :class="row.flag.key === focus ? 'bg-primary/5' : ''"
              class="grid grid-cols-[minmax(0,1fr)_auto] items-start gap-x-4 gap-y-1 border-b border-line px-5 py-3 last:border-b-0 sm:grid-cols-[minmax(0,14rem)_8rem_minmax(0,1fr)]"
            >
              <span class="flex min-w-0 flex-col">
                <span class="truncate text-xs font-medium text-ink">{{ row.name }}</span>
                <span class="truncate font-mono text-[11px] text-ink/45">{{ row.flag.key }}</span>
              </span>
              <span class="flex min-w-0 justify-end sm:justify-start"><FlagValue :value="row.answer.value" /></span>
              <span class="col-span-2 flex items-start gap-2 text-[11px] leading-4 text-ink/60 sm:col-span-1">
                <span :class="TONES[row.answer.resolution.kind]" aria-hidden="true" class="mt-1 size-1.5 flex-none rounded-full" />
                {{ why(row) }}
              </span>
            </li>
            <li v-if="!rows.length" class="px-5 py-10 text-center text-xs text-ink/55">
              Nothing published here yet: every flag answers with the game's own default.
            </li>
          </ul>
        </section>
      </div>

      <footer class="flex items-center justify-between gap-2 border-t border-line px-5 py-3">
        <span class="text-[11px] text-ink/45">Buckets are SHA-256 of project, flag, install and salt, as on the server.</span>
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

<style scoped>
.ps-preview-field :deep(.v-field__input),
.ps-preview-field :deep(.v-label) {
  font-size: 12px;
}

.ps-preview-switch :deep(.v-label) {
  font-size: 12px;
}
</style>
