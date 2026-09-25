<script setup lang="ts">
import type { LogStorage } from '~/composables/logs'

/**
 * A project's log storage: how long its logs are kept, what that holds now, what it took in over
 * the last month, and what logs have cost the organization this month on the two meters. The
 * retention is the one lever, so it sits first, and a lower choice says plainly what it deletes.
 *
 * Gigabytes here are the bill's: decimal, a billion bytes. Written in the panel's base-1024 bytes
 * they would disagree with the invoice by seven percent.
 */
const props = defineProps<{ path: string | null }>()
const open = defineModel<boolean>({ required: true })
const emit = defineEmits<{ changed: [] }>()

const read = useApiGet<LogStorage>(() => (open.value && props.path ? props.path : null))
const storage = computed(() => read.data.value)

const choice = ref(14)
watch(storage, (value) => {
  if (value) choice.value = value.retention_days
}, { immediate: true })

const OPTIONS = computed<SegmentedOption[]>(() => (storage.value?.retention_choices ?? [1, 3, 7, 14, 30, 60, 90])
  .map(days => ({ value: String(days), label: days === 1 ? '1 day' : `${days} days` })))
const choiceText = computed({
  get: () => String(choice.value),
  set: (value: string) => {
    choice.value = Number(value)
  }
})

const lowering = computed(() => storage.value !== undefined && storage.value !== null && choice.value < storage.value.retention_days)
const saving = ref(false)
const failure = ref('')

async function save() {
  if (!props.path) return
  saving.value = true
  failure.value = ''
  try {
    read.data.value = await apiRequest<LogStorage>('PUT', props.path, { days: choice.value })
    emit('changed')
  } catch (caught) {
    failure.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

const gb = (bytes: number) => bytes / 1e9
function gbWords(value: number): string {
  if (value === 0) return '0 GB'
  if (value < 0.01) return `${(value * 1000).toFixed(value < 0.001 ? 2 : 1)} MB`
  return `${value.toFixed(value < 10 ? 2 : 1)} GB`
}

interface Meter { label: string, used: number, included: number, unit: string, price: string, info: string }

const meters = computed<Meter[]>(() => {
  const s = storage.value
  if (!s) return []
  return [
    {
      label: 'Taken in',
      used: gb(s.ingested_bytes),
      included: gb(s.included_ingest_bytes),
      unit: 'GB',
      price: `${priceWords(s.ingest_price_per_gb_micros)} per GB past the free tier`,
      info: 'Log bytes every project sent this month, as the game sent them, uncompressed. Charged once, as they arrive.'
    },
    {
      label: 'Kept',
      used: s.gb_months,
      included: s.included_gb_months,
      unit: 'GB-months',
      price: `${priceWords(s.storage_price_per_gb_month_micros)} per GB-month past the free tier`,
      info: 'Gigabytes held each day, added up and divided by the month\'s days. One GB kept all month is one GB-month; a shorter retention keeps fewer.'
    }
  ]
})

const envWords: Record<string, string> = { production: 'Production', staging: 'Staging', development: 'Development' }

const dayTick = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })
const ingestDays = computed(() => {
  const held = new Map((storage.value?.ingested ?? []).map(point => [point.at.slice(0, 10), point.value]))
  const days: { day: string, value: number | null }[] = []
  const today = new Date()
  for (let back = 29; back >= 0; back--) {
    const at = new Date(Date.UTC(today.getUTCFullYear(), today.getUTCMonth(), today.getUTCDate() - back))
    const key = at.toISOString().slice(0, 10)
    days.push({ day: key, value: held.get(key) ?? 0 })
  }
  return days
})
/* In decimal megabytes, the bill's unit, rather than the chart's own base-1024 bytes. */
const ingestSeries = computed<MetricSeries[]>(() => [{ name: 'Taken in, MB', data: ingestDays.value.map(day => (day.value === null ? null : day.value / 1e6)), tone: 1 }])
const ingestCategories = computed(() => ingestDays.value.map(day => dayTick.format(new Date(day.day))))

const monthName = computed(() => storage.value ? new Date(storage.value.period_start).toLocaleString('en', { month: 'long', timeZone: 'UTC' }) : '')
</script>

<template>
  <v-dialog v-model="open" max-width="720" scrollable>
    <v-card class="flex flex-col">
      <header class="flex items-start gap-3 border-b border-line px-5 py-4">
        <div class="flex min-w-0 flex-1 flex-col gap-0.5">
          <h2 class="m-0 text-base font-medium text-ink">
            Log storage
          </h2>
          <p class="m-0 text-xs text-ink/60">
            How long this project keeps its logs, and what logs cost this month.
          </p>
        </div>
        <v-btn
          aria-label="Close"
          density="comfortable"
          icon="material-symbols:close"
          size="small"
          variant="text"
          @click="open = false"
        />
      </header>

      <div class="flex flex-col gap-6 overflow-y-auto px-5 py-5">
        <p v-if="read.error.value" class="m-0 text-xs text-error">
          {{ (read.error.value as Error).message }}
        </p>

        <!-- The lever. -->
        <section class="flex flex-col gap-3">
          <div class="flex flex-col gap-0.5">
            <h3 class="m-0 text-sm font-medium text-ink">
              Retention
            </h3>
            <p class="m-0 text-xs text-ink/60">
              Every record, exception and crash is kept this long, then deleted. Longer keeps more to search and costs more to keep.
            </p>
          </div>
          <SegmentedControl
            v-model="choiceText"
            :disabled="!storage || saving"
            :options="OPTIONS"
            label="Log retention"
            stretch
          />
          <InfoNote v-if="lowering">
            Records older than {{ RETENTION_WORDS[choice] ?? `${choice} days` }} are deleted over the next day, and stop counting toward storage at once. This cannot be undone.
          </InfoNote>
          <p v-else-if="storage?.retention_purge_until" class="m-0 text-[11px] text-ink/50">
            A shorter retention is still being applied to older records.
          </p>
          <div class="flex items-center justify-end gap-3">
            <span v-if="failure" class="text-xs text-error">{{ failure }}</span>
            <v-btn
              :disabled="!storage || choice === storage.retention_days"
              :loading="saving"
              class="text-xs font-medium"
              :color="lowering ? 'error' : 'primary'"
              size="small"
              variant="flat"
              @click="save"
            >
              {{ lowering ? 'Delete older records and save' : 'Save retention' }}
            </v-btn>
          </div>
        </section>

        <!-- What it holds. -->
        <section class="flex flex-col gap-3">
          <div class="flex items-baseline justify-between gap-3">
            <h3 class="m-0 text-sm font-medium text-ink">
              Held now
            </h3>
            <span class="font-mono text-sm text-ink">{{ storage ? gbWords(gb(storage.kept_bytes)) : '–' }}</span>
          </div>
          <ul v-if="storage" class="m-0 grid list-none grid-cols-3 gap-2 p-0">
            <li v-for="env in storage.environments" :key="env.environment" class="flex flex-col gap-0.5 rounded-(--ps-radius-small) border border-line px-3 py-2">
              <span class="text-[11px] text-ink/55">{{ envWords[env.environment] ?? env.environment }}</span>
              <span class="font-mono text-xs text-ink">{{ gbWords(gb(env.bytes)) }}</span>
            </li>
          </ul>
          <MetricChart
            v-if="storage"
            :categories="ingestCategories"
            :format="{ decimals: 1 }"
            :height="120"
            :series="ingestSeries"
            label="Megabytes of logs taken in each day over the last 30 days"
            type="bar"
          />
        </section>

        <!-- What it costs. -->
        <section class="flex flex-col gap-3">
          <div class="flex flex-col gap-0.5">
            <h3 class="m-0 text-sm font-medium text-ink">
              {{ monthName }} so far, every project
            </h3>
            <p class="m-0 text-xs text-ink/60">
              Two meters, each with a free tier every month. Charges come from the wallet as they accrue.
            </p>
          </div>
          <div class="grid gap-4 sm:grid-cols-2">
            <div v-for="meter in meters" :key="meter.label" class="flex flex-col gap-2 rounded-(--ps-radius) border border-line p-3">
              <div class="flex items-center gap-1.5 text-xs text-ink/60">
                {{ meter.label }}
                <v-tooltip :text="meter.info" location="top" max-width="280">
                  <template #activator="{ props: tip }">
                    <Icon
                      v-bind="tip"
                      aria-hidden="true"
                      class="text-sm text-ink/40"
                      name="material-symbols:info-outline"
                    />
                  </template>
                </v-tooltip>
              </div>
              <p class="m-0 flex items-baseline gap-1.5">
                <span class="font-mono text-lg text-ink tabular-nums">{{ meter.used.toFixed(meter.used < 10 ? 3 : 1) }}</span>
                <span class="text-xs text-ink/55">of {{ meter.included.toFixed(meter.included % 1 ? 2 : 0) }} {{ meter.unit }} free</span>
              </p>
              <div
                :aria-label="`${meter.label}: ${Math.round((meter.used / Math.max(meter.included, 1e-9)) * 100)}% of the free tier`"
                class="h-1.5 overflow-hidden rounded-full bg-ink/8"
                role="progressbar"
              >
                <div :class="meter.used > meter.included ? 'bg-warning' : 'bg-primary'" :style="{ width: `${Math.min(100, (meter.used / Math.max(meter.included, 1e-9)) * 100)}%` }" class="h-full rounded-full" />
              </div>
              <span class="text-[11px] text-ink/55">{{ meter.price }}</span>
              <span v-if="meter.unit === 'GB-months'" class="text-[11px] text-ink/40">Counted through yesterday; today is added tonight.</span>
            </div>
          </div>
          <dl v-if="storage" class="m-0 grid grid-cols-2 gap-px overflow-hidden rounded-(--ps-radius) border border-line bg-line">
            <div class="flex flex-col gap-0.5 bg-surface px-3 py-2.5">
              <dt class="text-[11px] text-ink/55">
                Charged so far
              </dt>
              <dd class="m-0 font-mono text-base text-ink">
                {{ priceWords(storage.charged_micros) }}
              </dd>
            </div>
            <div class="flex flex-col gap-0.5 bg-surface px-3 py-2.5">
              <dt class="text-[11px] text-ink/55">
                The month at this pace
              </dt>
              <dd class="m-0 font-mono text-base text-ink">
                {{ priceWords(storage.projected_micros) }}
              </dd>
            </div>
          </dl>
        </section>
      </div>
    </v-card>
  </v-dialog>
</template>
