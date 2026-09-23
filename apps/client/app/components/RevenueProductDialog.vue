<script setup lang="ts">
import type { ShareRow } from '~/components/ShareList.vue'

// One package, in detail: what it made and where it went — gross, less refunds, less the
// stores' fees, to net — how many sold, to how many players, and how many of them it
// made payers; its revenue day by day; which stores and countries bought it; and why the
// refunded ones were refunded. What a product manager opens before repricing a pack.
const props = defineProps<{
  line: ProductLine | null
  days: readonly number[]
}>()

const open = defineModel<boolean>({ required: true })

const dayFormat = new Intl.DateTimeFormat('en', { month: 'short', day: 'numeric', timeZone: 'UTC' })

const facts = computed<Fact[]>(() => {
  const line = props.line
  if (!line) return []
  const refundRate = line.transactions ? line.refunded / line.transactions : 0
  return [
    { key: 'gross', label: 'Gross', value: formatExactMoney(line.gross), mono: true, note: line.previousGross ? `${formatChange((line.gross - line.previousGross) / line.previousGross)} on the range before` : 'New this range' },
    { key: 'refunds', label: 'Refunded', value: formatExactMoney(line.refunds), mono: true, note: `${formatShare(refundRate)} of sales` },
    { key: 'fees', label: 'Store fees', value: formatExactMoney(line.fees), mono: true, note: 'What the stores kept' },
    { key: 'net', label: 'Net', value: formatExactMoney(line.net), mono: true, note: `${formatShare(line.gross ? line.net / line.gross : 0)} of gross` },
    { key: 'sold', label: 'Sold', value: writeFigure(Math.round(line.transactions)), mono: true, note: `to ${writeFigure(Math.round(line.buyers))} players` },
    { key: 'first', label: 'First purchases', value: writeFigure(Math.round(line.firstPurchases)), mono: true, note: 'Players it made payers' }
  ]
})

const series = computed<MetricSeries[]>(() => (props.line ? [{ name: 'Gross', data: props.line.daily.map(value => Math.round(value * 100) / 100), type: 'bar', tone: 1 }] : []))

const stores = computed<ShareRow[]>(() => {
  const line = props.line
  if (!line) return []
  const total = STORE_KEYS.reduce((sum, store) => sum + line.byStore[store], 0) || 1
  return STORE_KEYS.filter(store => line.byStore[store] > 0).map(store => ({
    key: store,
    label: STORES[store].label,
    value: line.byStore[store],
    display: formatMoney(line.byStore[store]),
    note: formatShare(line.byStore[store] / total),
    mark: { kind: 'platform' as const, code: STORES[store].platform }
  })).sort((a, b) => b.value - a.value)
})

const countries = computed<ShareRow[]>(() => {
  const line = props.line
  if (!line) return []
  return line.byCountry.slice(0, 6).map(entry => ({
    key: entry.code,
    label: segmentName('install_country', entry.code),
    value: entry.gross,
    display: formatMoney(entry.gross),
    note: formatShare(entry.gross / (line.gross || 1)),
    mark: { kind: 'country' as const, code: entry.code }
  }))
})

const reasons = computed<ShareRow[]>(() => (props.line?.reasons ?? [])
  .filter(entry => entry.count >= 0.5)
  .map(entry => ({ key: entry.reason, label: entry.reason, value: entry.count, display: writeFigure(Math.round(entry.count)) }))
  .sort((a, b) => b.value - a.value))
</script>

<template>
  <v-dialog v-model="open" max-width="880" scrollable>
    <v-card v-if="line" border flat>
      <header class="flex items-start justify-between gap-4 border-b border-line px-5 py-4">
        <div class="flex flex-col gap-1">
          <h2 class="m-0 flex items-center gap-2 text-base font-medium text-ink">
            {{ line.product.name }}
            <span class="rounded-(--ps-radius-x-small) bg-ink/5 px-1.5 text-[11px] font-normal text-ink/60">{{ KIND_LABELS[line.product.kind] }}</span>
          </h2>
          <p class="m-0 text-xs text-ink/55">
            <span class="font-mono">{{ line.product.id }}</span> · {{ formatExactMoney(line.product.price) }}
            <template v-if="line.product.window">
              · on sale {{ dayFormat.format(Date.now() - line.product.window[0] * 86_400_000) }} – {{ dayFormat.format(Date.now() - line.product.window[1] * 86_400_000) }}
            </template>
          </p>
        </div>
        <v-btn
          aria-label="Close"
          color="neutral"
          icon="material-symbols:close"
          size="small"
          variant="text"
          @click="open = false"
        />
      </header>

      <div class="flex flex-col gap-6 overflow-y-auto p-5">
        <FactGrid :columns="3" :facts="facts" :label="`${line.product.name} in figures`" />

        <MetricCard
          :categories="days.map(at => dayFormat.format(at))"
          :decimals="2"
          :height="180"
          :series="series"
          category-label="Day"
          description="Gross revenue, day by day."
          prefix="$"
          title="Revenue"
          type="bar"
        />

        <div class="grid gap-6 md:grid-cols-2">
          <section class="flex flex-col gap-2">
            <h3 class="m-0 text-xs font-medium text-ink/70">
              By store
            </h3>
            <ShareList :rows="stores" label="Revenue by store" />
          </section>
          <section class="flex flex-col gap-2">
            <h3 class="m-0 text-xs font-medium text-ink/70">
              Top countries
            </h3>
            <ShareList :rows="countries" label="Revenue by country" />
          </section>
        </div>

        <section v-if="reasons.length" class="flex flex-col gap-2">
          <h3 class="m-0 text-xs font-medium text-ink/70">
            Why it was refunded
          </h3>
          <ShareList :rows="reasons" label="Refunds by reason" tone="error" />
        </section>
      </div>
    </v-card>
  </v-dialog>
</template>
