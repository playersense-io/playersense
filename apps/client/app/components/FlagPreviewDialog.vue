<script setup lang="ts">
// What one player would be served, flag by flag, and why: which rule answered, or which rollout
// left them out and at what bucket. The same evaluation the server runs on /v1/config — the
// first matching rule, a rule's rollout answering with the fallback rather than falling
// through, SHA-256 buckets keyed by the install — against the draft or against whatever an
// environment serves now.
//
// The player is described the way the server sees one, in four parts by who says so: the
// device, the connection, their history and the game's own context. A sample player fills
// the fields in; each stays editable. A player who has not signed in has no `.player.`
// history, as on the server, and the install id — which decides every rollout — is one line
// at the foot, with a button to draw another.
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
const SOURCES = computed(() => [
  { value: 'draft', title: 'The draft', subtitle: 'Everything saved, published or not' },
  ...ENVIRONMENTS.filter(environment => latest(environment)).map(environment => ({
    value: environment,
    title: ENVIRONMENT_LABELS[environment],
    subtitle: `What it serves now, v${latest(environment)!.version}`
  }))
])

/* Rollouts are drawn from the install id, so a new one is a player drawn again. */
const newInstall = () => crypto.randomUUID()

const install = ref(newInstall())
const signedIn = ref(false)
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
  value: string
  title: string
  subtitle: string
  spend: string
  purchases: string
  daysSinceInstall: string
  daysSinceSeen: string
  sessions: string
  level: string
  signedIn: boolean
}

/**
 * Players worth checking, each described by what they are rather than named for a segment:
 * choosing one fills in the history and the level below, which stay editable.
 */
const PERSONAS: Persona[] = [
  { value: 'new', title: 'A new player', subtitle: 'Installed yesterday, level 4, has never paid', spend: '0', purchases: '0', daysSinceInstall: '1', daysSinceSeen: '0', sessions: '3', level: '4', signedIn: false },
  { value: 'payer', title: 'A regular payer', subtitle: 'Two months in, level 35, spent $24.97 over 3 purchases', spend: '24.97', purchases: '3', daysSinceInstall: '60', daysSinceSeen: '0', sessions: '180', level: '35', signedIn: true },
  { value: 'whale', title: 'A big spender', subtitle: 'Seven months in, level 87, spent $240 over 31 purchases', spend: '240', purchases: '31', daysSinceInstall: '210', daysSinceSeen: '0', sessions: '640', level: '87', signedIn: true },
  { value: 'lapsed', title: 'A returning player', subtitle: 'Back after 3 weeks away, level 23, one purchase', spend: '9.99', purchases: '1', daysSinceInstall: '120', daysSinceSeen: '21', sessions: '48', level: '23', signedIn: true }
]

const persona = ref<string | null>('new')

function apply(value: string | null) {
  persona.value = value
  const chosen = PERSONAS.find(entry => entry.value === value)
  if (!chosen) return
  spend.value = chosen.spend
  purchases.value = chosen.purchases
  daysSinceInstall.value = chosen.daysSinceInstall
  daysSinceSeen.value = chosen.daysSinceSeen
  sessions.value = chosen.sessions
  signedIn.value = chosen.signedIn
  const level = context.value.find(entry => entry.key === 'progress.level')
  if (level) level.value = chosen.level
  else context.value.push({ key: 'progress.level', value: chosen.level })
}

type Group = 'device' | 'connection' | 'history' | 'context'

const openGroup = ref<Group | null>('history')

const deviceSummary = computed(() => [
  PLATFORM_NAMES[platform.value] ?? platform.value,
  appVersion.value,
  LOCALES.find(entry => entry.value === locale.value)?.title ?? locale.value
].filter(Boolean).join(' · '))

const connectionSummary = computed(() => `${country.value ? countryName(country.value) : 'Unknown'}${vpn.value ? ', through a VPN' : ''}`)

const historySummary = computed(() => {
  const bought = Number(purchases.value) || 0
  const money = bought ? `$${spend.value} over ${bought} ${bought === 1 ? 'purchase' : 'purchases'}` : 'never paid'
  return `Day ${daysSinceInstall.value}, ${money}${signedIn.value ? ', signed in' : ''}`
})

const contextSummary = computed(() => {
  const set = context.value.filter(entry => entry.key.trim() && entry.value.trim())
  return set.length ? set.slice(0, 3).map(entry => `${entry.key.replace(/^progress\./, '')} ${entry.value}`).join(', ') + (set.length > 3 ? '…' : '') : 'Nothing set'
})

const LOCALES = ['en-US', 'en-GB', 'de-DE', 'fr-FR', 'es-ES', 'pt-BR', 'ja-JP', 'ko-KR', 'tr-TR', 'zh-CN'].map((value) => {
  const language = new Intl.DisplayNames(['en'], { type: 'language' }).of(value) ?? value
  return { value, title: language, subtitle: value }
})

const PLATFORM_NAMES: Record<string, string> = {
  ios: 'iOS', android: 'Android', windows: 'Windows', macos: 'macOS', linux: 'Linux', web: 'Web', console: 'Console'
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
  if (signedIn.value) history('player')
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

const PLATFORM_ITEMS = TARGET_PLATFORMS.map(value => ({ value, title: PLATFORM_NAMES[value] ?? value }))
const COUNTRY_ITEMS = COUNTRY_SUGGESTIONS.map(code => ({ value: code, title: countryName(code) }))
</script>

<template>
  <v-dialog v-model="open" max-width="1080" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          Preview a player
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          Describe a player and see what every flag serves them, and why — worked out the way the server works it out.
        </p>
      </header>

      <div class="grid min-h-0 overflow-y-auto lg:grid-cols-[22rem_minmax(0,1fr)]">
        <aside class="flex flex-col gap-6 border-b border-line p-5 lg:border-r lg:border-b-0">
          <div class="flex flex-col gap-4">
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-medium text-ink" for="preview-source">Answer from</label>
              <v-select
                id="preview-source"
                v-model="source"
                :items="SOURCES"
                v-bind="FIELD_MENU"
                density="comfortable"
                hide-details
                item-props
                variant="outlined"
              />
            </div>
            <div class="flex flex-col gap-1.5">
              <label class="text-xs font-medium text-ink" for="preview-persona">Start from</label>
              <v-select
                id="preview-persona"
                :items="PERSONAS"
                v-bind="FIELD_MENU"
                :model-value="persona"
                density="comfortable"
                hide-details
                item-props
                placeholder="A player of your own"
                variant="outlined"
                @update:model-value="apply"
              />
              <span class="text-[11px] leading-4 text-ink/50">Fills in the fields below. Change any of them after.</span>
            </div>
          </div>

          <!-- The player in four parts, by who says so. One is open at a time; the others say
               what they are set to, so the whole player reads without opening any. -->
          <div class="flex flex-col border-t border-line">
            <FoldGroup
              :open="openGroup === 'device'"
              :summary="deviceSummary"
              title="Their device"
              @update:open="openGroup = $event ? 'device' : null"
            >
              <template #mark>
                <AuthorityMark authority="sdk" />
              </template>
              <div class="grid grid-cols-2 gap-3">
                <div class="flex min-w-0 flex-col gap-1.5">
                  <label class="text-[11px] text-ink/55" for="preview-platform">Platform</label>
                  <v-select
                    id="preview-platform"
                    v-model="platform"
                    :items="PLATFORM_ITEMS"
                    v-bind="FIELD_MENU"
                    density="comfortable"
                    hide-details
                    variant="outlined"
                  />
                </div>
                <div class="flex min-w-0 flex-col gap-1.5">
                  <label class="text-[11px] text-ink/55" for="preview-version">Game version</label>
                  <v-text-field
                    id="preview-version"
                    v-model="appVersion"
                    class="font-mono"
                    density="comfortable"
                    hide-details
                    variant="outlined"
                  />
                </div>
              </div>
              <div class="flex flex-col gap-1.5">
                <label class="text-[11px] text-ink/55" for="preview-locale">Language</label>
                <v-select
                  id="preview-locale"
                  v-model="locale"
                  :items="LOCALES"
                  v-bind="FIELD_MENU"
                  density="comfortable"
                  hide-details
                  item-props
                  variant="outlined"
                />
              </div>
            </FoldGroup>

            <FoldGroup
              :open="openGroup === 'connection'"
              :summary="connectionSummary"
              title="Where they connect from"
              @update:open="openGroup = $event ? 'connection' : null"
            >
              <template #mark>
                <AuthorityMark authority="connection" />
              </template>
              <div class="flex flex-col gap-1.5">
                <label class="text-[11px] text-ink/55" for="preview-country">Country</label>
                <v-autocomplete
                  id="preview-country"
                  v-model="country"
                  :items="COUNTRY_ITEMS"
                  v-bind="FIELD_MENU"
                  density="comfortable"
                  hide-details
                  variant="outlined"
                />
              </div>
              <SwitchRow v-model="vpn" label="Through a VPN" note="PlayerSense sees the VPN's country, not theirs." />
            </FoldGroup>

            <FoldGroup
              :open="openGroup === 'history'"
              :summary="historySummary"
              title="What they have done"
              @update:open="openGroup = $event ? 'history' : null"
            >
              <template #mark>
                <AuthorityMark authority="history" />
              </template>
              <div class="grid grid-cols-2 gap-3">
                <div class="flex min-w-0 flex-col gap-1.5">
                  <label class="text-[11px] text-ink/55" for="preview-spend">Spent in total</label>
                  <v-text-field
                    id="preview-spend"
                    v-model="spend"
                    density="comfortable"
                    hide-details
                    inputmode="decimal"
                    prefix="$"
                    variant="outlined"
                    @update:model-value="persona = null"
                  />
                </div>
                <div class="flex min-w-0 flex-col gap-1.5">
                  <label class="text-[11px] text-ink/55" for="preview-purchases">Purchases</label>
                  <v-text-field
                    id="preview-purchases"
                    v-model="purchases"
                    density="comfortable"
                    hide-details
                    inputmode="numeric"
                    variant="outlined"
                    @update:model-value="persona = null"
                  />
                </div>
                <div class="flex min-w-0 flex-col gap-1.5">
                  <label class="text-[11px] text-ink/55" for="preview-installed">Days since install</label>
                  <v-text-field
                    id="preview-installed"
                    v-model="daysSinceInstall"
                    density="comfortable"
                    hide-details
                    inputmode="numeric"
                    variant="outlined"
                    @update:model-value="persona = null"
                  />
                </div>
                <div class="flex min-w-0 flex-col gap-1.5">
                  <label class="text-[11px] text-ink/55" for="preview-away">Days away before today</label>
                  <v-text-field
                    id="preview-away"
                    v-model="daysSinceSeen"
                    density="comfortable"
                    hide-details
                    inputmode="numeric"
                    variant="outlined"
                    @update:model-value="persona = null"
                  />
                </div>
                <div class="flex min-w-0 flex-col gap-1.5">
                  <label class="text-[11px] text-ink/55" for="preview-sessions">Sessions played</label>
                  <v-text-field
                    id="preview-sessions"
                    v-model="sessions"
                    density="comfortable"
                    hide-details
                    inputmode="numeric"
                    variant="outlined"
                    @update:model-value="persona = null"
                  />
                </div>
              </div>
              <SwitchRow
                v-model="signedIn"
                label="Signed in to an account"
                note="Their history is then counted across every device they play on."
              />
            </FoldGroup>

            <FoldGroup
              :open="openGroup === 'context'"
              :summary="contextSummary"
              title="What the game says about them"
              @update:open="openGroup = $event ? 'context' : null"
            >
              <template #mark>
                <AuthorityMark authority="client" />
              </template>
              <div v-for="(entry, index) in context" :key="index" class="flex items-center gap-2">
                <v-text-field
                  v-model="entry.key"
                  :aria-label="`Context key ${index + 1}`"
                  class="min-w-0 flex-1 font-mono"
                  density="comfortable"
                  hide-details
                  placeholder="key"
                  variant="outlined"
                />
                <v-text-field
                  v-model="entry.value"
                  :aria-label="`Context value ${index + 1}`"
                  class="w-28 flex-none"
                  density="comfortable"
                  hide-details
                  placeholder="value"
                  variant="outlined"
                />
                <v-btn
                  :aria-label="`Remove ${entry.key || 'context key'}`"
                  color="neutral"
                  icon="material-symbols:close"
                  size="small"
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
                Add a key
              </v-btn>
            </FoldGroup>
          </div>

          <p class="m-0 flex items-center justify-between gap-2 border-t border-line pt-4 text-[11px] leading-4 text-ink/50">
            <span>Rollouts are drawn by install. This one is <span class="font-mono text-ink/70">{{ install.slice(0, 8) }}</span>.</span>
            <v-btn
              class="flex-none text-xs"
              color="neutral"
              prepend-icon="material-symbols:casino-outline"
              size="small"
              variant="text"
              @click="install = newInstall()"
            >
              Draw again
            </v-btn>
          </p>
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
