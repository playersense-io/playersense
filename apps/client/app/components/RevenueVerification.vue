<script setup lang="ts">
// What the SDK's purchase verification did with the receipts it was sent: how many it
// verified with the store, how many it turned away and why — a forged receipt, a
// transaction already granted once and sent again, a receipt altered after the store
// signed it — the test purchases it kept out of revenue, and how long each store took to
// answer. Nothing turned away reaches revenue, and a replayed transaction is caught by
// the one-row-per-store-transaction rule (project.md §46–48), so a retry never grants
// twice.
const props = defineProps<{
  sample: RevenueSample
  categories: readonly string[]
  loading?: boolean
}>()

const rejected = computed(() => props.sample.verification.rejected.reduce((sum, entry) => sum + entry.count, 0))
const attempts = computed(() => props.sample.verification.verified + rejected.value + props.sample.verification.sandbox)

const facts = computed<Fact[]>(() => {
  const { verification } = props.sample
  const median = verification.byStore.length
    ? [...verification.byStore].sort((a, b) => a.medianMs - b.medianMs)[Math.floor(verification.byStore.length / 2)]!.medianMs
    : 0
  return [
    { key: 'verified', label: 'Verified', value: writeFigure(Math.round(verification.verified)), mono: true, note: `${formatShare(verification.verified / Math.max(attempts.value, 1))} of receipts sent` },
    { key: 'rejected', label: 'Turned away', value: writeFigure(Math.round(rejected.value)), mono: true, note: `${formatShare(rejected.value / Math.max(attempts.value, 1))} of receipts sent` },
    { key: 'sandbox', label: 'Test purchases', value: writeFigure(Math.round(verification.sandbox)), mono: true, note: 'Verified, and kept out of revenue' },
    { key: 'time', label: 'Median time to verify', value: `${Math.round(median)} ms`, mono: true, note: 'Receipt in to answer out' }
  ]
})

const series = computed<MetricSeries[]>(() => [{
  name: 'Turned away',
  data: props.sample.verification.daily.map(day => Math.round(day.rejected)),
  type: 'bar',
  tone: 2
}])
</script>

<template>
  <div class="flex flex-col gap-5">
    <SectionCard description="Every receipt the SDK sent is checked with its store before it counts. What is turned away never reaches revenue." title="Purchase verification">
      <FactGrid :columns="4" :facts="facts" label="Verification in figures" />
    </SectionCard>

    <div class="grid gap-5 lg:grid-cols-2">
      <SectionCard :padded="false" description="Why receipts were turned away." title="Turned away">
        <table class="ps-revenue-verify">
          <tbody>
            <tr v-for="entry in sample.verification.rejected" :key="entry.reason">
              <td>
                <span class="block text-ink">{{ entry.reason }}</span>
                <span class="block text-[11px] text-ink/55">{{ entry.note }}</span>
              </td>
              <td class="font-mono">
                {{ writeFigure(Math.round(entry.count)) }}
              </td>
              <td class="font-mono text-ink/55">
                {{ formatShare(entry.count / Math.max(rejected, 1)) }}
              </td>
            </tr>
          </tbody>
        </table>
      </SectionCard>

      <SectionCard :padded="false" description="How each store's receipts fared, and how long the store took to answer." title="By store">
        <table class="ps-revenue-verify">
          <thead>
            <tr>
              <th>Store</th>
              <th>Verified</th>
              <th>Turned away</th>
              <th>Median time</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="entry in sample.verification.byStore" :key="entry.store">
              <td>
                <span class="inline-flex items-center gap-1.5">
                  <PlatformIcon :platform="STORES[entry.store].platform" />{{ STORES[entry.store].label }}
                </span>
              </td>
              <td class="font-mono">
                {{ writeFigure(Math.round(entry.verified)) }}
              </td>
              <td class="font-mono text-ink/70">
                {{ formatShare(entry.rejected / Math.max(entry.verified + entry.rejected, 1)) }}
              </td>
              <td class="font-mono text-ink/70">
                {{ Math.round(entry.medianMs) }} ms
              </td>
            </tr>
          </tbody>
        </table>
      </SectionCard>
    </div>

    <MetricCard
      :categories="categories"
      :decimals="0"
      :loading="loading"
      :series="series"
      category-label="Day"
      description="Receipts turned away each day. A sudden rise is someone trying forged or replayed receipts against the game."
      title="Turned away by day"
      type="bar"
    />
  </div>
</template>

<style scoped>
.ps-revenue-verify {
  border-collapse: separate;
  border-spacing: 0;
  font-size: 12px;
  width: 100%;
}

.ps-revenue-verify th {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 400;
  padding: 10px 12px;
  text-align: left;
}

.ps-revenue-verify td {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 10px 12px;
}

/* The name reads from the left, the figures from the right. */
.ps-revenue-verify :is(th, td):not(:first-child) {
  text-align: right;
}

.ps-revenue-verify :is(th, td):first-child {
  padding-left: 20px;
}

.ps-revenue-verify :is(th, td):last-child {
  padding-right: 20px;
}

.ps-revenue-verify tbody tr:last-child td {
  border-bottom: 0;
}
</style>
