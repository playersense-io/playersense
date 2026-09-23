<script setup lang="ts">
// A new test, in draft: what it is called and what it expects, the flag it runs on, the
// variants (the control is the flag's value today; each other variant is a value to try), how
// it is decided, and for how long. The split is even and says so; a test decided by one metric
// and watched on a few more is the standard shape, and the defaults take it.
const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{ created: [id: string] }>()

const { flags } = useLiveConfig()
const { experiments, create } = useExperiments()

const name = ref('')
const hypothesis = ref('')
const flagKey = ref<string | null>(null)
const audience = ref('Every player')
const primaryMetric = ref<MetricKey>('revenue_per_player')
const guardrails = ref<MetricKey[]>(['d7_retention'])
const plannedDays = ref<number>(14)
interface VariantDraft {
  id: string
  /** Empty for none: the variant is then called by its letter. */
  name: string
  value: Json
  weight: number
  invalid: boolean
}

/* Typed through the cast: unwrapping `Json` through a ref recurses without end. */
const variants = ref([]) as Ref<VariantDraft[]>

/* A flag that already carries a running test is not offered: two tests on one flag would each
   spoil the other. */
const busy = computed(() => new Set(experiments.value.filter(entry => entry.status === 'running').map(entry => entry.flagKey)))

const FLAG_ITEMS = computed(() => flags.value
  .filter(flag => !flag.archived)
  .map(flag => ({
    value: flag.key,
    title: flag.name,
    subtitle: busy.value.has(flag.key) ? `${flag.key} · a test is running on it` : flag.key,
    props: { disabled: busy.value.has(flag.key) }
  })))

const flag = computed(() => flags.value.find(entry => entry.key === flagKey.value) ?? null)

/* Choosing a flag sets the variants: its value today as the control, and one to try. */
watch(flagKey, () => {
  const current = flag.value
  if (!current) return
  const other = current.kind === 'bool' ? !current.fallback : servedValues(documentOf(current)).find(value => !sameJson(value, current.fallback)) ?? defaultValue(current.kind)
  variants.value = [
    { id: 'a', name: '', value: current.fallback, weight: 50, invalid: false },
    { id: 'b', name: '', value: other, weight: 50, invalid: false }
  ]
})

function addVariant() {
  if (!flag.value || variants.value.length >= 4) return
  const id = 'abcd'[variants.value.length]!
  variants.value = [...variants.value, { id, name: '', value: defaultValue(flag.value.kind), weight: 0, invalid: false }]
  splitEvenly()
}

function removeVariant(index: number) {
  variants.value = variants.value.filter((_, at) => at !== index)
  splitEvenly()
}

/* An even split that adds up to 100: the first variants take the remainder. */
function splitEvenly() {
  const count = variants.value.length || 1
  const base = Math.floor(100 / count)
  variants.value = variants.value.map((entry, index) => ({ ...entry, weight: base + (index < 100 - base * count ? 1 : 0) }))
}

const splitTotal = computed(() => variants.value.reduce((sum, entry) => sum + (entry.weight || 0), 0))

/* The share of the audience let in, in hundredths, as the share field and the slider hold it. */
const traffic = ref(10_000)

const METRIC_ITEMS = METRIC_KEYS.map(key => ({ value: key, title: EXPERIMENT_METRICS[key].label, subtitle: EXPERIMENT_METRICS[key].note }))

const guardrailItems = computed(() => METRIC_ITEMS.filter(item => item.value !== primaryMetric.value))

watch(primaryMetric, (value) => {
  guardrails.value = guardrails.value.filter(entry => entry !== value)
})

const duplicate = computed(() => {
  const seen = variants.value.map(entry => JSON.stringify(entry.value))
  return seen.some((value, index) => seen.indexOf(value) !== index)
})

const ready = computed(() => name.value.trim() !== '' && flag.value !== null && variants.value.length >= 2
  && !variants.value.some(entry => entry.invalid || entry.weight < 1) && splitTotal.value === 100 && !duplicate.value && plannedDays.value >= MIN_DAYS && traffic.value >= 100)

function confirm() {
  if (!ready.value || !flag.value) return
  const id = create({
    name: name.value.trim(),
    hypothesis: hypothesis.value.trim(),
    flagKey: flag.value.key,
    audience: audience.value.trim() || 'Every player',
    traffic: Math.round(traffic.value / 100),
    primaryMetric: primaryMetric.value,
    guardrails: guardrails.value,
    variants: variants.value.map((entry, index) => ({ id: entry.id, name: entry.name.trim() || variantLetter(index), value: entry.value, weight: entry.weight, control: index === 0 })),
    plannedDays: plannedDays.value
  })
  emit('created', id)
  open.value = false
}

const ids = { name: useId(), hypothesis: useId(), flag: useId(), audience: useId(), traffic: useId(), metric: useId(), guardrails: useId(), days: useId() }
</script>

<template>
  <v-dialog v-model="open" max-width="680" scrollable>
    <v-card border flat>
      <header class="flex flex-col gap-1 border-b border-line p-5">
        <h2 class="m-0 text-sm font-medium text-ink">
          New experiment
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          It starts as a draft. Nothing reaches players until you start it and publish its flag.
        </p>
      </header>

      <form class="flex flex-col gap-6 overflow-y-auto p-5" @submit.prevent="confirm">
        <div class="flex flex-col gap-1.5">
          <label :for="ids.name" class="text-xs font-medium text-ink">Name</label>
          <v-text-field
            :id="ids.name"
            v-model="name"
            autocomplete="off"
            autofocus
            hide-details
            maxlength="120"
            placeholder="Cheaper starter pack"
            variant="outlined"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.hypothesis" class="text-xs font-medium text-ink">What you expect <span class="font-normal text-ink/45">the question the result answers</span></label>
          <v-textarea
            :id="ids.hypothesis"
            v-model="hypothesis"
            auto-grow
            hide-details
            maxlength="500"
            placeholder="A cheaper starter pack turns more new players into payers, and pays for itself."
            rows="2"
            variant="outlined"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.flag" class="text-xs font-medium text-ink">Flag it runs on</label>
          <span class="text-[11px] leading-4 text-ink/55">Each variant is a value this flag serves. The server keeps each player in one variant.</span>
          <v-autocomplete
            :id="ids.flag"
            v-model="flagKey"
            v-bind="FIELD_MENU"
            :items="FLAG_ITEMS"
            hide-details
            item-props
            placeholder="Choose a flag"
            variant="outlined"
          />
        </div>

        <div v-if="flag" class="flex flex-col gap-2">
          <div class="flex items-baseline justify-between gap-2">
            <span class="text-xs font-medium text-ink">Variants</span>
            <span class="text-[11px] text-ink/50">A is the control: what the game does today</span>
          </div>
          <ul class="m-0 flex list-none flex-col gap-2 p-0">
            <li
              v-for="(entry, index) in variants"
              :key="entry.id"
              class="flex flex-col gap-3 rounded-(--ps-radius-large) border border-line p-3"
            >
              <div class="flex items-center gap-2">
                <span
                  :style="{ background: `rgb(var(--v-theme-chart-${index + 1}))` }"
                  aria-hidden="true"
                  class="flex size-7 flex-none items-center justify-center rounded-(--ps-radius-small) font-mono text-xs font-medium text-white"
                >{{ variantLetter(index) }}</span>
                <v-text-field
                  v-model="entry.name"
                  :aria-label="`Variant ${variantLetter(index)} name`"
                  :placeholder="`Variant ${variantLetter(index)}, or a name of your own`"
                  class="min-w-0 flex-1"
                  density="comfortable"
                  hide-details
                  maxlength="60"
                  variant="outlined"
                />
                <v-text-field
                  v-model.number="entry.weight"
                  :aria-label="`Variant ${variantLetter(index)} share of the test`"
                  :error="entry.weight < 1"
                  class="ps-new-experiment__weight w-24 flex-none"
                  density="comfortable"
                  hide-details
                  inputmode="numeric"
                  suffix="%"
                  type="number"
                  variant="outlined"
                />
                <v-btn
                  v-if="index > 0 && variants.length > 2"
                  :aria-label="`Remove variant ${variantLetter(index)}`"
                  color="neutral"
                  icon="material-symbols:close"
                  size="small"
                  variant="text"
                  @click="removeVariant(index)"
                />
              </div>
              <div class="flex flex-col gap-1.5">
                <span class="text-[11px] text-ink/55">{{ index === 0 ? 'Serves, as the flag does today' : 'Serves' }}</span>
                <FlagValueEditor
                  v-model="entry.value"
                  v-model:invalid="entry.invalid"
                  :kind="flag.kind"
                  :label="`Variant ${variantLetter(index)} value`"
                  block
                  dense
                />
              </div>
            </li>
          </ul>

          <div class="flex flex-wrap items-center justify-between gap-2">
            <span :class="splitTotal === 100 ? 'text-ink/55' : 'text-error'" class="text-[11px]">
              {{ variants.map(entry => `${entry.weight || 0}%`).join(' + ') }} = {{ splitTotal }}%{{ splitTotal === 100 ? '' : ', and a split must add up to 100%' }}
            </span>
            <div class="flex items-center gap-1">
              <v-btn
                class="text-xs"
                color="neutral"
                size="small"
                variant="text"
                @click="splitEvenly"
              >
                Split evenly
              </v-btn>
              <v-btn
                v-if="variants.length < 4"
                class="text-xs"
                color="neutral"
                prepend-icon="material-symbols:add"
                size="small"
                variant="text"
                @click="addVariant"
              >
                Add a variant
              </v-btn>
            </div>
          </div>
          <p v-if="duplicate" class="m-0 text-xs text-error">
            Two variants serve the same value, so the test could not tell them apart.
          </p>
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.audience" class="text-xs font-medium text-ink">Who is in it</label>
          <v-text-field
            :id="ids.audience"
            v-model="audience"
            hide-details
            maxlength="120"
            placeholder="Every player"
            variant="outlined"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.traffic" class="text-xs font-medium text-ink">Traffic</label>
          <span class="text-[11px] leading-4 text-ink/55">
            The share of them let into the test. The rest keep what the game serves today and are not counted.
            Start small for a risky change, and raise it once nothing breaks.
          </span>
          <div class="flex items-center gap-3">
            <ShareField :id="ids.traffic" v-model="traffic" label="Traffic" />
            <v-slider
              :model-value="traffic / 100"
              aria-label="Traffic"
              class="min-w-0 flex-1"
              color="primary"
              hide-details
              max="100"
              min="1"
              step="1"
              thumb-size="14"
              track-size="4"
              @update:model-value="traffic = Math.round($event) * 100"
            />
          </div>
          <span class="text-[11px] text-ink/55">
            {{ Math.round(traffic / 100) }}% in the test<template v-if="variants.length">: {{ variants.map((entry, index) => `${variantLetter(index)} gets ${Math.round((traffic / 100) * (entry.weight || 0)) / 100}% of the audience`).join(', ') }}</template>.
          </span>
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.metric" class="text-xs font-medium text-ink">Decided by</label>
          <span class="text-[11px] leading-4 text-ink/55">The one metric the winner is picked on. Choose it now, not after the results are in.</span>
          <v-select
            :id="ids.metric"
            v-model="primaryMetric"
            v-bind="FIELD_MENU"
            :items="METRIC_ITEMS"
            hide-details
            item-props
            variant="outlined"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.guardrails" class="text-xs font-medium text-ink">Must not get worse</label>
          <span class="text-[11px] leading-4 text-ink/55">Guardrails: the page warns if a variant makes one of these worse, whatever it does to the metric above.</span>
          <v-select
            :id="ids.guardrails"
            v-model="guardrails"
            v-bind="FIELD_MENU"
            :items="guardrailItems"
            chips
            closable-chips
            hide-details
            item-props
            multiple
            variant="outlined"
          />
        </div>

        <div class="flex flex-col gap-1.5">
          <label :for="ids.days" class="text-xs font-medium text-ink">Runs for</label>
          <span class="text-[11px] leading-4 text-ink/55">At least a week, so every day of the week is in it; two weeks is the usual minimum for retention.</span>
          <div class="flex items-center gap-3">
            <v-number-input
              :id="ids.days"
              v-model="plannedDays"
              :max="90"
              :min="MIN_DAYS"
              class="w-44"
              control-variant="split"
              hide-details
              inset
              variant="outlined"
            />
            <span class="text-xs text-ink/60">days</span>
          </div>
        </div>
      </form>

      <footer class="flex items-center justify-end gap-2 border-t border-line px-5 py-3">
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
          @click="confirm"
        >
          Create experiment
        </v-btn>
      </footer>
    </v-card>
  </v-dialog>
</template>

<style scoped>
/* A share is read as a figure; the browser's steppers are dropped for the field's own look. */
.ps-new-experiment__weight :deep(input) {
  font-family: var(--font-mono);
  -moz-appearance: textfield;
}

.ps-new-experiment__weight :deep(input::-webkit-inner-spin-button),
.ps-new-experiment__weight :deep(input::-webkit-outer-spin-button) {
  -webkit-appearance: none;
  margin: 0;
}
</style>
