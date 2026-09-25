<script setup lang="ts">
// What purchase verification did with the receipts it was sent: how many it verified with the
// store, how many it turned away and why (a forged receipt, a transaction already granted once
// and sent again, a receipt for another app), the test purchases it kept out of revenue, and how
// long each store took to answer. Nothing turned away reaches revenue, and a replayed transaction
// is caught by the one-row-per-store-transaction rule, so a retry never grants twice.
const props = defineProps<{
  revenue: RevenueRecord | null
  loading?: boolean
}>()

const verification = computed(() => props.revenue?.verification ?? null)

const attempts = computed(() => {
  const v = verification.value
  return v ? v.verified + v.rejected : 0
})

const facts = computed<Fact[]>(() => {
  const v = verification.value
  if (!v) return []
  const times = v.by_store.map(entry => entry.median_ms).filter(ms => ms > 0).sort((a, b) => a - b)
  const median = times.length ? times[Math.floor(times.length / 2)]! : 0
  return [
    { key: 'verified', label: 'Verified', value: writeFigure(v.verified), mono: true, note: `${formatShare(v.verified / Math.max(attempts.value, 1))} of receipts sent, tests included` },
    { key: 'rejected', label: 'Turned away', value: writeFigure(v.rejected), mono: true, note: `${formatShare(v.rejected / Math.max(attempts.value, 1))} of receipts sent` },
    { key: 'sandbox', label: 'Test purchases', value: writeFigure(v.sandbox), mono: true, note: 'Verified, and kept out of revenue' },
    { key: 'time', label: 'Median time to verify', value: median ? `${Math.round(median)} ms` : 'None yet', mono: true, note: 'The store\'s answer, the middle store' }
  ]
})

const categories = computed(() => (verification.value?.days ?? []).map(day => dayLabel(day.day)))
const series = computed<MetricSeries[]>(() => [
  { name: 'Verified', data: (verification.value?.days ?? []).map(day => day.verified), type: 'bar', stack: 'receipts', tone: 1 },
  { name: 'Turned away', data: (verification.value?.days ?? []).map(day => day.rejected), type: 'bar', stack: 'receipts', tone: 2 }
])
</script>

<template>
  <div class="flex flex-col gap-5">
    <SectionCard v-if="!verification" title="Purchase verification">
      <EmptyState
        description="Connect App Store, Google Play or Steam, and every receipt the game sends is checked with its store before it counts. Revenue then reads the verified purchases instead of what the game reported."
        icon="material-symbols:verified-outline"
        title="No store is verified yet."
      >
        <template #actions>
          <v-btn
            class="text-xs font-medium"
            color="primary"
            size="small"
            to="/settings/stores"
            variant="flat"
          >
            Connect a store
          </v-btn>
        </template>
      </EmptyState>
    </SectionCard>

    <template v-else>
      <SectionCard description="Every receipt the game sent is checked with its store before it counts. What is turned away never reaches revenue." title="Purchase verification">
        <FactGrid :columns="4" :facts="facts" label="Verification in figures" />
      </SectionCard>

      <div class="grid gap-5 lg:grid-cols-2">
        <SectionCard :padded="false" description="Why receipts were turned away." title="Turned away">
          <p v-if="!verification.rejections.length" class="m-0 px-5 py-4 text-xs text-ink/55">
            Nothing was turned away in the range.
          </p>
          <table v-else class="ps-revenue-verify">
            <tbody>
              <tr v-for="entry in verification.rejections" :key="entry.code">
                <td>
                  <span class="block text-ink">{{ rejectionLook(entry.code).label }}</span>
                  <span class="block text-[11px] text-ink/55">{{ rejectionLook(entry.code).note }}</span>
                </td>
                <td class="font-mono">
                  {{ writeFigure(entry.count) }}
                </td>
                <td class="font-mono text-ink/55">
                  {{ formatShare(entry.count / Math.max(verification.rejected, 1)) }}
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
              <tr v-for="entry in verification.by_store" :key="entry.store">
                <td>
                  <span class="inline-flex items-center gap-1.5">
                    <PlatformIcon :platform="storeLook(entry.store).platform" />{{ storeLook(entry.store).label }}
                  </span>
                </td>
                <td class="font-mono">
                  {{ writeFigure(entry.verified) }}
                </td>
                <td class="font-mono text-ink/70">
                  {{ formatShare(entry.rejected / Math.max(entry.verified + entry.rejected, 1)) }}
                </td>
                <td class="font-mono text-ink/70">
                  {{ Math.round(entry.median_ms) }} ms
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
        description="Receipts verified and turned away each day. A sudden rise in the second is someone trying forged or replayed receipts against the game."
        title="Receipts by day"
        type="bar"
      />
    </template>
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
