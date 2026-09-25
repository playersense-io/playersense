<script setup lang="ts">
// A destination, made or edited on a page of its own: where events go, which ones, whose, and
// how they are sent, in four sections read top to bottom. Beside them, the destination as it
// stands, what is still missing, and the buttons, in view however far the form scrolls.
//
// The same form makes a new destination and edits one; `destination` is the one being edited.
const props = defineProps<{
  destination?: WebhookDestination | null
}>()

/** `reset` asks the page to draw the form again from what was saved; `cancel` to leave it. */
const emit = defineEmits<{ saved: [id: string], reset: [], cancel: [] }>()

const { create, update, destinations } = useWebhooks()

const editing = computed(() => Boolean(props.destination))
const base = props.destination

// --- The fields ---------------------------------------------------------------------------

const url = ref(base?.url ?? '')
const name = ref(base?.name ?? '')
const description = ref(base?.description ?? '')
const events = ref<string[]>(base ? [...base.events] : [])
const environments = ref<string[]>(base ? [...base.environments] : ['production'])
const headers = ref<WebhookHeader[]>(base ? base.headers.map(header => ({ ...header })) : [])

const urlTouched = ref(editing.value)
const urlError = computed(() => (urlTouched.value ? urlProblem(url.value) : null))

/* The name follows the URL's host until somebody writes one of their own. */
const nameTouched = ref(editing.value)
watch(url, (value) => {
  if (!nameTouched.value && !urlProblem(value)) name.value = hostOf(value)
})

const duplicate = computed(() => destinations.value.find(entry => entry.id !== props.destination?.id && entry.url.trim() === url.value.trim()) ?? null)

const ENVIRONMENT_CHOICES = computed(() => ENVIRONMENTS.slice().reverse().map(value => ({
  value,
  label: ENVIRONMENT_LABELS[value],
  note: ({
    production: 'Real players: real purchases, the published config.',
    staging: 'QA and testers: sandbox purchases, staging publishes.',
    development: 'Your team\'s devices and the editor.'
  } as Record<EnvironmentKind, string>)[value]
})))

/* The example body: one of the events chosen, or any event while none is. */
const exampleTypes = computed(() => {
  const chosen = eventsOf(events.value)
  return chosen.length ? chosen : WEBHOOK_EVENTS.map(entry => entry.type)
})
const EXAMPLE_OPTIONS = computed<GhostSelectOption[]>(() => exampleTypes.value.map(type => ({ value: type, label: type, group: WEBHOOK_GROUP[WEBHOOK_EVENT[type]!.group].label })))
const pickedExample = ref<string | undefined>(undefined)
const exampleType = computed({
  get: () => (pickedExample.value && exampleTypes.value.includes(pickedExample.value) ? pickedExample.value : exampleTypes.value[0]!),
  set: (value: string) => {
    pickedExample.value = value
  }
})
const example = computed(() => exampleBody(exampleType.value, (environments.value[0] ?? 'production') as EnvironmentKind))

/* A secret header saved before: its value is not read back, and left blank it keeps what it has. */
const savedSecret = (name: string) => Boolean(base?.headers.some(header => header.secret && header.name.toLowerCase() === name.trim().toLowerCase()))

function addHeader() {
  headers.value = [...headers.value, { name: '', value: '', secret: false }]
}

function removeHeader(index: number) {
  headers.value = headers.value.filter((_, position) => position !== index)
}

/* The headers PlayerSense sets itself, which a header of the studio's cannot replace. */
const RESERVED = ['content-type', 'user-agent', 'x-playersense-id', 'x-playersense-timestamp', 'x-playersense-signature', 'host', 'content-length']

const headerProblem = computed(() => {
  for (const header of headers.value) {
    const key = header.name.trim().toLowerCase()
    if (!key && !header.value.trim()) continue
    if (!key) return 'A header with a value and no name'
    if (!/^[A-Za-z0-9-]+$/.test(header.name.trim())) return `"${header.name}" is not a header name: letters, digits and dashes`
    if (RESERVED.includes(key)) return `${header.name} is set by PlayerSense`
    if (header.secret && !header.value && !savedSecret(header.name)) return `The secret header ${header.name} needs a value`
  }
  const names = headers.value.map(header => header.name.trim().toLowerCase()).filter(Boolean)
  if (new Set(names).size !== names.length) return 'The same header twice'
  return null
})

// --- Saving -------------------------------------------------------------------------------

const missing = computed(() => {
  const out: { section: string, text: string }[] = []
  const problem = urlProblem(url.value)
  if (problem) out.push({ section: 'where', text: problem })
  else if (duplicate.value) out.push({ section: 'where', text: `${duplicate.value.name} already sends to this URL` })
  if (name.value.trim().length < 2) out.push({ section: 'where', text: 'A name of two characters or more' })
  if (!events.value.length) out.push({ section: 'what', text: 'At least one event to send' })
  if (!environments.value.length) out.push({ section: 'whose', text: 'At least one environment' })
  if (headers.value.filter(header => header.name.trim()).length > 10) out.push({ section: 'how', text: 'Up to ten custom headers' })
  if (headerProblem.value) out.push({ section: 'how', text: headerProblem.value })
  return out
})

const ready = computed(() => !missing.value.length)

function draft(): WebhookDraft {
  return {
    name: name.value.trim(),
    description: description.value.trim(),
    url: url.value.trim(),
    events: events.value.includes(EVERY_EVENT) ? [EVERY_EVENT] : WEBHOOK_EVENTS.map(entry => entry.type).filter(type => events.value.includes(type)),
    environments: ENVIRONMENTS.filter(value => environments.value.includes(value)).reverse(),
    headers: headers.value.filter(header => header.name.trim()).map(header => ({ name: header.name.trim(), value: header.value, secret: Boolean(header.secret) }))
  }
}

const saving = ref(false)
const problem = ref('')

async function save() {
  urlTouched.value = true
  if (!ready.value || saving.value) return
  saving.value = true
  problem.value = ''
  try {
    let id: string
    if (props.destination) {
      await update(props.destination, draft())
      id = props.destination.id
    } else {
      id = await create(draft())
    }
    saved.value = true
    emit('saved', id)
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

// --- Changes, and leaving with them -------------------------------------------------------

const snapshot = () => JSON.stringify([url.value, name.value, description.value, events.value, environments.value, headers.value])
const initial = snapshot()
const dirty = computed(() => snapshot() !== initial)
const saved = ref(false)

/* The yes is a flag of its own: the dialog clears `leaving` as it confirms (see LiveOpsEventForm). */
const leaving = ref<string | null>(null)
let leaveConfirmed = false

const leaveOpen = computed({
  get: () => leaving.value !== null,
  set: (value) => {
    if (!value) leaving.value = null
  }
})

onBeforeRouteLeave((to) => {
  if (saved.value || !dirty.value || leaveConfirmed) return true
  leaving.value = to.fullPath
  return false
})

const router = useRouter()

function leave() {
  const to = leaving.value
  leaveConfirmed = true
  leaving.value = null
  if (to) router.push(to)
}

const guard = (event: BeforeUnloadEvent) => {
  if (dirty.value && !saved.value) event.preventDefault()
}
onMounted(() => window.addEventListener('beforeunload', guard))
onBeforeUnmount(() => window.removeEventListener('beforeunload', guard))

// --- The summary --------------------------------------------------------------------------

const touchedGroups = computed(() => groupsOf(events.value))

function jump(section: string) {
  document.getElementById(`section-${section}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const ids = { url: useId(), name: useId(), description: useId() }

const SECTIONS = [
  { id: 'where', number: 1, title: 'Where to send them' },
  { id: 'what', number: 2, title: 'Which events' },
  { id: 'whose', number: 3, title: 'From which environments' },
  { id: 'how', number: 4, title: 'How they are sent' }
]
</script>

<template>
  <form class="grid items-start gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]" @submit.prevent="save">
    <div class="flex min-w-0 flex-col gap-6">
      <!-- 1. Where. -->
      <section :id="`section-${SECTIONS[0]!.id}`" :aria-labelledby="`heading-${SECTIONS[0]!.id}`" class="flex scroll-mt-20 flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <h2 :id="`heading-${SECTIONS[0]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
          <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">1</span>
          {{ SECTIONS[0]!.title }}
        </h2>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.url" class="text-xs font-medium text-ink">Endpoint URL <span class="font-normal text-ink/45">on your own server, public, https</span></label>
          <v-text-field
            :id="ids.url"
            v-model="url"
            :error="Boolean(urlError) || Boolean(duplicate)"
            autocomplete="off"
            autofocus
            class="font-mono"
            hide-details
            inputmode="url"
            placeholder="https://api.yourgame.com/playersense/webhooks"
            spellcheck="false"
            variant="outlined"
            @blur="urlTouched = true"
          />
          <span v-if="urlError" class="text-[11px] text-error">{{ urlError }}</span>
          <span v-else-if="duplicate" class="text-[11px] text-error">
            <NuxtLink :to="`/webhooks/${duplicate.id}`" class="text-error underline underline-offset-2">{{ duplicate.name }}</NuxtLink> already sends to this URL. Add the events to it instead.
          </span>
          <span v-else class="text-[11px] text-ink/50">Every event is POSTed here as JSON. Answer with any 2xx within 10 seconds, and do the work after.</span>
        </div>

        <div class="grid gap-4 sm:grid-cols-2">
          <div class="flex flex-col gap-1.5">
            <label :for="ids.name" class="text-xs font-medium text-ink">Name</label>
            <v-text-field
              :id="ids.name"
              v-model="name"
              autocomplete="off"
              hide-details
              maxlength="80"
              placeholder="Game server: grant purchases"
              variant="outlined"
              @update:model-value="nameTouched = true"
            />
          </div>
          <div class="flex flex-col gap-1.5">
            <label :for="ids.description" class="text-xs font-medium text-ink">Description <span class="font-normal text-ink/45">optional, for the team</span></label>
            <v-text-field
              :id="ids.description"
              v-model="description"
              autocomplete="off"
              hide-details
              maxlength="240"
              placeholder="Who owns it, and what it does with the events"
              variant="outlined"
            />
          </div>
        </div>
      </section>

      <!-- 2. Which events. -->
      <section :id="`section-${SECTIONS[1]!.id}`" :aria-labelledby="`heading-${SECTIONS[1]!.id}`" class="flex scroll-mt-20 flex-col gap-4 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <div class="flex flex-col gap-1">
          <h2 :id="`heading-${SECTIONS[1]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
            <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">2</span>
            {{ SECTIONS[1]!.title }}
          </h2>
          <p class="m-0 pl-8.5 text-xs text-ink/55">
            What happens in the project, not every event a player sends: those stay in Analytics, and Report Builder hands them over in bulk.
          </p>
        </div>
        <WebhookEventPicker v-model="events" />
      </section>

      <!-- 3. Whose. -->
      <section :id="`section-${SECTIONS[2]!.id}`" :aria-labelledby="`heading-${SECTIONS[2]!.id}`" class="flex scroll-mt-20 flex-col gap-4 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <div class="flex flex-col gap-1">
          <h2 :id="`heading-${SECTIONS[2]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
            <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">3</span>
            {{ SECTIONS[2]!.title }}
          </h2>
          <p class="m-0 pl-8.5 text-xs text-ink/55">
            A test build's purchases are sandbox purchases. Keep them away from the server that grants real ones: give staging a destination of its own.
          </p>
        </div>
        <CheckList v-model="environments" :options="ENVIRONMENT_CHOICES" label="Environments" />
        <p v-if="environments.includes('production') && environments.length > 1" class="m-0 flex items-start gap-2 rounded-(--ps-radius) bg-warning/8 px-3 py-2.5 text-xs leading-5 text-ink/80 ring-1 ring-warning/20 ring-inset">
          <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" name="material-symbols:warning-outline" />
          Production and test builds to one URL: your server has to read project.environment on every event, or a sandbox purchase is granted like a real one.
        </p>
      </section>

      <!-- 4. How. -->
      <section :id="`section-${SECTIONS[3]!.id}`" :aria-labelledby="`heading-${SECTIONS[3]!.id}`" class="flex scroll-mt-20 flex-col gap-5 rounded-(--ps-radius-x-large) border border-line bg-surface p-5">
        <h2 :id="`heading-${SECTIONS[3]!.id}`" class="m-0 flex items-center gap-2.5 text-sm font-medium text-ink">
          <span class="flex size-6 items-center justify-center rounded-full bg-ink/6 font-mono text-[11px] text-ink/70">4</span>
          {{ SECTIONS[3]!.title }}
        </h2>

        <!-- What the server receives, for one of the events chosen. -->
        <div class="flex flex-col gap-2">
          <div class="flex flex-wrap items-center justify-between gap-2">
            <span class="flex flex-col gap-0.5">
              <span class="text-xs font-medium text-ink">What your server receives</span>
              <span class="text-[11px] text-ink/50">An example body, {{ events.length ? 'for an event you chose' : 'for any event' }}. Every body has the same envelope; data is the event's own, and a test event carries exactly this.</span>
            </span>
            <GhostSelect
              v-model="exampleType"
              :options="EXAMPLE_OPTIONS"
              label="Example event"
              size="small"
            />
          </div>
          <CodeBlock :code="example" :title="`${exampleType} · version ${WEBHOOK_API_VERSION}`" class="max-h-96" />
        </div>

        <!-- Headers of the studio's own, for a gateway in front of the URL. -->
        <div class="flex flex-col gap-2">
          <span class="flex flex-col gap-0.5">
            <span class="text-xs font-medium text-ink">Custom headers <span class="font-normal text-ink/45">optional</span></span>
            <span class="text-[11px] text-ink/50">Up to ten, sent with every delivery, for a gateway or firewall in front of your server that wants a token of its own. A secret one is stored encrypted and never shown again.</span>
          </span>
          <ul v-if="headers.length" class="m-0 flex list-none flex-col gap-2 p-0">
            <li v-for="(header, index) in headers" :key="index" class="grid grid-cols-[minmax(0,1fr)_minmax(0,1.4fr)_auto_auto] items-center gap-2">
              <v-text-field
                v-model="header.name"
                :aria-label="`Header ${index + 1} name`"
                autocomplete="off"
                class="font-mono"
                density="comfortable"
                hide-details
                placeholder="X-Gateway-Token"
                spellcheck="false"
                variant="outlined"
              />
              <v-text-field
                v-model="header.value"
                :aria-label="`Header ${index + 1} value`"
                :placeholder="header.secret && savedSecret(header.name) ? 'Set. Leave blank to keep it' : 'Value'"
                :type="header.secret ? 'password' : 'text'"
                autocomplete="off"
                class="font-mono"
                density="comfortable"
                hide-details
                spellcheck="false"
                variant="outlined"
              />
              <v-checkbox
                v-model="header.secret"
                class="flex-none"
                density="compact"
                hide-details
                label="Secret"
              />
              <v-btn
                :aria-label="`Remove header ${header.name || index + 1}`"
                color="neutral"
                icon="material-symbols:close"
                size="small"
                variant="text"
                @click="removeHeader(index)"
              />
            </li>
          </ul>
          <span v-if="headerProblem" class="text-[11px] text-error">{{ headerProblem }}</span>
          <div>
            <v-btn
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:add"
              size="small"
              variant="outlined"
              @click="addHeader"
            >
              Add header
            </v-btn>
          </div>
        </div>

        <!-- What is not a setting: the same for every destination, said once so nobody looks for it. -->
        <div class="grid gap-3 rounded-(--ps-radius-large) bg-ink/3 p-4 sm:grid-cols-3">
          <div class="flex flex-col gap-0.5">
            <span class="text-[11px] text-ink/50">Answer within</span>
            <span class="text-xs text-ink">10 seconds, with any 2xx</span>
          </div>
          <div class="flex flex-col gap-0.5">
            <span class="text-[11px] text-ink/50">Retries</span>
            <span class="text-xs text-ink">{{ WEBHOOK_TRIES - 1 }} more over 3 days, then it fails</span>
          </div>
          <div class="flex flex-col gap-0.5">
            <span class="text-[11px] text-ink/50">Turned off</span>
            <span class="text-xs text-ink">After 3 days of nothing but failures</span>
          </div>
        </div>
      </section>
    </div>

    <!-- The destination as it stands, what is missing, and the buttons. -->
    <aside class="flex flex-col gap-3 lg:sticky lg:top-20">
      <section aria-label="Summary" class="flex flex-col overflow-hidden rounded-(--ps-radius-x-large) border border-line bg-surface">
        <header class="flex items-center gap-3 border-b border-line p-4">
          <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-primary/10 text-lg text-primary">
            <Icon name="material-symbols:webhook" />
          </span>
          <div class="flex min-w-0 flex-col">
            <span :class="name.trim() ? 'text-ink' : 'text-ink/40'" class="truncate text-sm font-medium">{{ name.trim() || 'New destination' }}</span>
            <span class="truncate font-mono text-[11px] text-ink/45">{{ url.trim() && !urlProblem(url) ? hostOf(url) : 'no URL yet' }}</span>
          </div>
        </header>

        <dl class="m-0 flex flex-col divide-y divide-line/70">
          <button class="flex cursor-pointer flex-col gap-1 px-4 py-3 text-left hover:bg-ink/2" type="button" @click="jump('what')">
            <dt class="text-[11px] text-ink/45">
              Sends
            </dt>
            <dd :class="events.length ? 'text-ink' : 'text-ink/40'" class="m-0 text-xs leading-5">
              {{ events.length ? listeningWords(events) : 'No events yet' }}
            </dd>
            <dd v-if="touchedGroups.length && !events.includes(EVERY_EVENT)" class="m-0 flex flex-wrap gap-1">
              <span v-for="entry in touchedGroups" :key="entry.group.key" class="rounded-full bg-ink/5 px-1.5 text-[10px] leading-4 text-ink/65">
                {{ entry.group.label }} <span class="font-mono text-ink/45">{{ entry.taken }}/{{ entry.total }}</span>
              </span>
            </dd>
          </button>
          <button class="flex cursor-pointer flex-col gap-0.5 px-4 py-3 text-left hover:bg-ink/2" type="button" @click="jump('whose')">
            <dt class="text-[11px] text-ink/45">
              From
            </dt>
            <dd class="m-0 text-xs leading-5 text-ink">
              {{ environments.length ? ENVIRONMENTS.filter(value => environments.includes(value)).reverse().map(value => ENVIRONMENT_LABELS[value]).join(', ') : 'No environment yet' }}
            </dd>
          </button>
          <button class="flex cursor-pointer flex-col gap-0.5 px-4 py-3 text-left hover:bg-ink/2" type="button" @click="jump('how')">
            <dt class="text-[11px] text-ink/45">
              Sent as
            </dt>
            <dd class="m-0 text-xs leading-5 text-ink">
              Version <span class="font-mono">{{ WEBHOOK_API_VERSION }}</span>
            </dd>
            <dd class="m-0 text-[11px] text-ink/50">
              {{ headers.filter(header => header.name.trim()).length ? `${headers.filter(header => header.name.trim()).length} custom ${headers.filter(header => header.name.trim()).length === 1 ? 'header' : 'headers'}` : 'No custom headers' }}
            </dd>
          </button>
        </dl>

        <div class="border-t border-line p-4">
          <ul v-if="missing.length" class="m-0 flex list-none flex-col gap-1.5 p-0">
            <li v-for="item in missing" :key="item.text">
              <button class="flex cursor-pointer items-start gap-2 text-left text-xs leading-4 text-ink/75 hover:text-ink" type="button" @click="jump(item.section)">
                <Icon aria-hidden="true" class="mt-px flex-none text-sm text-warning" name="material-symbols:error-outline" />
                {{ item.text }}
              </button>
            </li>
          </ul>
          <p v-else class="m-0 flex items-center gap-2 text-xs text-success">
            <Icon aria-hidden="true" class="flex-none text-sm" name="material-symbols:check-circle-outline" />
            Ready to {{ editing ? 'save' : 'add' }}
          </p>
          <p v-if="problem" class="m-0 mt-2 text-xs text-error">
            {{ problem }}
          </p>
        </div>

        <footer class="flex flex-col gap-2 border-t border-line p-4">
          <v-btn
            :disabled="!ready || (editing && !dirty)"
            :loading="saving"
            block
            class="text-xs font-medium"
            color="primary"
            type="submit"
            variant="flat"
          >
            {{ editing ? 'Save changes' : 'Add destination' }}
          </v-btn>
          <div class="flex items-center justify-between gap-2">
            <v-btn
              :disabled="!dirty"
              class="text-xs"
              color="neutral"
              prepend-icon="material-symbols:restart-alt"
              size="small"
              variant="text"
              @click="emit('reset')"
            >
              Reset
            </v-btn>
            <v-btn
              class="text-xs"
              color="neutral"
              size="small"
              variant="text"
              @click="emit('cancel')"
            >
              Cancel
            </v-btn>
          </div>
        </footer>
      </section>

      <p class="m-0 px-1 text-[11px] leading-4 text-ink/50">
        <template v-if="editing">
          Saved changes apply from the next event. Deliveries already made keep what they were sent with.
        </template>
        <template v-else>
          It is enabled as it is added, with a signing secret of its own. Send it a test event from its page before real ones arrive.
        </template>
      </p>
    </aside>

    <ConfirmDialog
      v-if="leaveOpen"
      v-model="leaveOpen"
      action="Leave without saving"
      description="What was changed here is not saved anywhere, and goes when you leave."
      title="Leave without saving?"
      @confirm="leave"
    />
  </form>
</template>
