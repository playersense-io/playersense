<script setup lang="ts">
// A new key, in two steps. First what it is: its kind, which decides everything it may do,
// its environment and a name. Then the key itself, shown this once, with where it goes and
// how: only a digest of its private half is kept, so nobody can show it again, us included.
//
// Rolling a key opens the same dialog with the old key's kind, environment and name, and the
// new key takes its place: the old one stops working the moment the new one is made.
const props = defineProps<{
  /** The kind to start on, from a kind's own Create button. */
  kind?: ApiKeyKind
  /** The key being rolled. */
  replaces?: ApiKeyDef | null
}>()

const open = defineModel<boolean>({ required: true })

const { create } = useApiKeys()

const step = ref<'form' | 'reveal'>('form')

const kind = ref<string | null>(props.replaces?.kind ?? props.kind ?? 'project')
const environment = ref<string>(props.replaces?.environment ?? 'production')
const name = ref(props.replaces ? `${props.replaces.name} (new)` : '')

const KIND_OPTIONS = computed<ChoiceOption[]>(() => API_KEY_KIND_ORDER.map(value => ({
  value,
  label: `${API_KEY_KINDS[value].label} · ${API_KEY_KINDS[value].where.toLowerCase()}`,
  note: API_KEY_KINDS[value].can,
  icon: API_KEY_KINDS[value].icon
})))

const ENVIRONMENT_OPTIONS: SegmentedOption[] = [
  { value: 'development', label: 'Development' },
  { value: 'staging', label: 'Staging' },
  { value: 'production', label: 'Production' }
]

const chosenKind = computed(() => (kind.value ?? 'project') as ApiKeyKind)
const chosenEnvironment = computed(() => environment.value as EnvironmentKind)

const ready = computed(() => name.value.trim().length >= 2 && kind.value !== null)

const created = ref<{ key: ApiKeyDef, secret: string } | null>(null)
const copied = ref(false)

function submit() {
  if (!ready.value) return
  created.value = create(chosenKind.value, chosenEnvironment.value, name.value.trim(), props.replaces?.id)
  step.value = 'reveal'
}

async function copy() {
  if (!created.value) return
  try {
    await navigator.clipboard.writeText(created.value.secret)
  } catch {
    /* The clipboard can be refused; the key is on screen to select by hand. */
  }
  copied.value = true
}

const usage = computed(() => (created.value ? usageCode(created.value.key.kind, created.value.secret) : null))

const ids = { name: useId() }
</script>

<template>
  <v-dialog
    v-model="open"
    :persistent="step === 'reveal'"
    max-width="640"
    scrollable
  >
    <v-card border flat>
      <template v-if="step === 'form'">
        <header class="flex flex-col gap-1 border-b border-line p-5">
          <h2 class="m-0 text-sm font-medium text-ink">
            {{ replaces ? `Roll ${replaces.name}` : 'Create a key' }}
          </h2>
          <p class="m-0 text-xs text-ink/60">
            <template v-if="replaces">
              A new key of the same kind and environment takes its place. {{ replaces.name }} stops working the moment the new one is made.
            </template>
            <template v-else>
              A key is one kind in one environment. Make one for each place it goes, so each can be revoked alone.
            </template>
          </p>
        </header>

        <div class="overflow-y-auto">
          <form class="flex flex-col gap-5 p-5" @submit.prevent="submit">
            <p v-if="replaces && replaces.kind === 'project'" class="m-0 flex items-start gap-2 rounded-(--ps-radius) bg-warning/8 px-3 py-2.5 text-xs leading-5 text-ink/80 ring-1 ring-warning/20 ring-inset">
              <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" name="material-symbols:warning-outline" />
              <span>Builds already out carry {{ replaces.name }}. Rolled, it stops working for them at once: they send no events and fetch no config until players update to a build with the new key.</span>
            </p>
            <div class="flex flex-col gap-1.5">
              <span class="text-xs font-medium text-ink">Kind</span>
              <ChoiceList
                v-model="kind"
                :disabled="Boolean(replaces)"
                :options="KIND_OPTIONS"
                label="Kind"
              />
              <span :class="API_KEY_KINDS[chosenKind].secret ? 'text-ink/55' : 'text-warning'" class="flex items-start gap-1.5 text-[11px] leading-4">
                <Icon :name="API_KEY_KINDS[chosenKind].secret ? 'material-symbols:lock-outline' : 'material-symbols:lock-open-outline'" aria-hidden="true" class="mt-px flex-none text-sm" />
                {{ API_KEY_KINDS[chosenKind].cannot }}
              </span>
            </div>

            <div class="flex flex-col gap-1.5">
              <span class="text-xs font-medium text-ink">Environment</span>
              <SegmentedControl
                v-model="environment"
                :disabled="Boolean(replaces)"
                :options="ENVIRONMENT_OPTIONS"
                label="Environment"
              />
              <span class="text-[11px] text-ink/55">{{ ENVIRONMENT_NOTES[chosenEnvironment] }} It only ever sees this environment's data and config.</span>
            </div>

            <div class="flex flex-col gap-1.5">
              <label :for="ids.name" class="text-xs font-medium text-ink">Name <span class="font-normal text-ink/45">where it is used, so its row is recognisable</span></label>
              <v-text-field
                :id="ids.name"
                v-model="name"
                autocomplete="off"
                autofocus
                hide-details
                maxlength="120"
                :placeholder="chosenKind === 'project' ? 'Release builds' : chosenKind === 'server' ? 'Game server' : 'Looker dashboards'"
                variant="outlined"
              />
            </div>

            <div class="flex flex-col gap-1 rounded-(--ps-radius) bg-ink/3 px-3 py-2.5">
              <span class="text-[11px] text-ink/50">It will start</span>
              <span class="font-mono text-xs text-ink">{{ publicPrefix(chosenKind, chosenEnvironment) }}<span class="text-ink/35">••••••••••:••••••••</span></span>
              <span class="text-[11px] text-ink/50">The start names its kind and environment, so a key in the wrong place is plain to see.</span>
            </div>
          </form>
        </div>

        <footer class="flex justify-end gap-2 border-t border-line px-5 py-3">
          <v-btn
            class="text-xs font-medium"
            color="neutral"
            size="small"
            variant="text"
            @click="open = false"
          >
            Cancel
          </v-btn>
          <v-btn
            :disabled="!ready"
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="submit"
          >
            {{ replaces ? 'Create the new key' : 'Create key' }}
          </v-btn>
        </footer>
      </template>

      <!-- The key, this once. -->
      <template v-else-if="created && usage">
        <header class="flex items-start gap-3 border-b border-line p-5">
          <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-success/12 text-lg text-success">
            <Icon name="material-symbols:key-outline" />
          </span>
          <div class="flex min-w-0 flex-col gap-1">
            <h2 class="m-0 text-sm font-medium text-ink">
              {{ created.key.name }} is ready
            </h2>
            <p class="m-0 text-xs text-ink/60">
              {{ API_KEY_KINDS[created.key.kind].label }} for {{ ENVIRONMENT_LABELS[created.key.environment].toLowerCase() }}.
            </p>
          </div>
        </header>

        <div class="overflow-y-auto">
          <div class="flex flex-col gap-4 p-5">
            <p class="m-0 flex items-start gap-2 rounded-(--ps-radius) bg-warning/8 px-3 py-2.5 text-xs leading-5 text-ink/80 ring-1 ring-warning/20 ring-inset">
              <Icon aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" name="material-symbols:warning-outline" />
              <span>Copy it now. This is the only time it is shown: only a digest is kept, so nobody can show it again, us included. Lose it and you make a new one.</span>
            </p>

            <div class="flex items-center gap-2 rounded-(--ps-radius) border border-line bg-ink/2 py-1.5 pr-1.5 pl-3">
              <code class="min-w-0 flex-1 font-mono text-xs break-all text-ink">{{ created.secret }}</code>
              <v-btn
                :color="copied ? 'success' : 'primary'"
                :prepend-icon="copied ? 'material-symbols:check' : 'material-symbols:content-copy-outline'"
                class="flex-none text-xs font-medium"
                size="small"
                variant="flat"
                @click="copy"
              >
                {{ copied ? 'Copied' : 'Copy' }}
              </v-btn>
            </div>

            <div class="flex flex-col gap-2">
              <span class="text-xs font-medium text-ink">Where it goes</span>
              <CodeBlock :code="usage.code" :lang="usage.lang" :title="usage.title" />
            </div>

            <p v-if="replaces" class="m-0 text-[11px] leading-4 text-ink/55">
              {{ replaces.name }} was rolled and no longer works. Put the new key wherever the old one was.
            </p>
          </div>
        </div>

        <footer class="flex items-center justify-between gap-3 border-t border-line px-5 py-3">
          <v-checkbox
            v-model="copied"
            density="compact"
            hide-details
            label="I have stored it somewhere safe"
          />
          <v-btn
            :disabled="!copied"
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="open = false"
          >
            Done
          </v-btn>
        </footer>
      </template>
    </v-card>
  </v-dialog>
</template>
