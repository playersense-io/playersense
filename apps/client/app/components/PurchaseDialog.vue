<script setup lang="ts">
// One purchase, in full: what was bought, by whom, where and for how much, and what happened to
// it step by step (bought, checked with the store, granted or turned away, refunded), then every
// identifier somebody would quote to a store's support, and the row as the purchases API returns
// it.
const props = defineProps<{
  record: PurchaseRow | null
}>()

const open = defineModel<boolean>({ required: true })

const when = (at: string) => new Date(at).toLocaleString('en', { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

const state = computed(() => purchaseState(props.record?.status ?? ''))
const paid = computed(() => (props.record ? writeAmount(props.record.revenue_micros, props.record.currency) : ''))

interface Step {
  icon: string
  tone: string
  title: string
  detail: string
  at?: string
}

/* What happened to it, in order. */
const steps = computed<Step[]>(() => {
  const record = props.record
  if (!record) return []
  const store = storeLook(record.store).label
  const out: Step[] = [
    { icon: 'material-symbols:shopping-cart-outline', tone: 'text-ink/60', title: `Bought in ${store}`, detail: `${record.product_id} for ${paid.value}`, at: record.at }
  ]
  if (record.source !== 'verified') {
    out.push({ icon: 'material-symbols:info-outline', tone: 'text-ink/60', title: 'Reported by the game', detail: `${store} is not verified for this project, so the purchase counts as the game reported it.` })
    return out
  }
  if (record.status === 'rejected') {
    out.push({ icon: 'material-symbols:gpp-bad-outline', tone: 'text-error', title: 'Turned away', detail: `${rejectionLook(record.rejection ?? '').label}. Nothing was granted and nothing counted.` })
    return out
  }
  out.push({ icon: 'material-symbols:verified-outline', tone: 'text-success', title: `Verified with ${store}`, detail: record.verify_ms ? `The store confirmed it in ${record.verify_ms} ms.` : 'The store confirmed it.' })
  if (record.status === 'sandbox') {
    out.push({ icon: 'material-symbols:science-outline', tone: 'text-ink/60', title: 'A test purchase', detail: 'Made by a tester account: granted in the game, kept out of revenue.' })
    return out
  }
  out.push({ icon: 'material-symbols:redeem-outline', tone: 'text-primary', title: 'Counted as revenue', detail: `${record.usd != null ? `${writeExactMoney(record.usd)} at the day's rate` : paid.value}${record.first_purchase ? ', and the player\'s first purchase ever' : ''}.` })
  if (record.status === 'refunded') {
    const hours = record.refunded_at ? Math.max(1, Math.round((Date.parse(record.refunded_at) - Date.parse(record.at)) / 3_600_000)) : null
    out.push({ icon: 'material-symbols:undo', tone: 'text-warning', title: 'Refunded', detail: `${record.refund_reason || 'Refunded'}${hours ? `, ${hours} hours after it was bought` : ''}.`, at: record.refunded_at ?? undefined })
  }
  return out
})

const facts = computed<Fact[]>(() => {
  const record = props.record
  if (!record) return []
  const out: Fact[] = []
  if (record.player_id) out.push({ key: 'player', label: 'Player', value: record.player_id, mono: true, to: `/analytics/players/${encodeURIComponent(record.player_id)}`, copy: record.player_id })
  out.push(
    { key: 'install', label: 'Install', value: record.install_id, mono: true, copy: record.install_id },
    { key: 'country', label: 'Country', value: record.country ? countryName(record.country) : 'Unknown' },
    { key: 'store', label: 'Store', value: `${storeLook(record.store).label} on ${record.platform}` },
    { key: 'package', label: 'Package', value: record.product_id, mono: true },
    { key: 'paid', label: 'Paid', value: record.usd != null && record.currency !== 'USD' ? `${paid.value} · ${writeExactMoney(record.usd)}` : paid.value },
    { key: 'version', label: 'App version', value: record.app_version || 'Unknown', mono: Boolean(record.app_version) }
  )
  if (record.store_transaction_id) out.push({ key: 'transaction', label: 'Store transaction', value: record.store_transaction_id, mono: true, copy: record.store_transaction_id, info: 'The store\'s own id. Quote it to the store\'s support; a retry with it is recognised and never granted twice.' })
  out.push({ key: 'id', label: 'Event id', value: record.event_id, mono: true, copy: record.event_id })
  return out
})

const code = computed(() => (props.record ? JSON.stringify(props.record, null, 2) : ''))
</script>

<template>
  <v-dialog v-model="open" max-width="720" scrollable>
    <v-card v-if="record" border flat>
      <header class="flex items-start justify-between gap-4 border-b border-line p-5">
        <div class="flex min-w-0 flex-col gap-1">
          <span class="flex items-center gap-2">
            <h2 class="m-0 truncate font-mono text-sm font-medium text-ink">
              {{ record.product_id }}
            </h2>
            <StatusBadge :label="state.label" :tone="state.tone" />
          </span>
          <span class="text-xs text-ink/55">{{ state.note }}</span>
        </div>
        <div class="flex flex-none flex-col items-end">
          <span class="font-mono text-lg text-ink">{{ paid }}</span>
          <span v-if="record.currency !== 'USD' && record.usd != null" class="font-mono text-[11px] text-ink/45">{{ writeExactMoney(record.usd) }}</span>
        </div>
      </header>

      <!-- The scroller holds one plain block, as tall as what is in it, so nothing inside is squeezed
           to fit the dialog's height. -->
      <div class="overflow-y-auto">
        <div class="flex flex-col gap-6 p-5">
          <!-- What happened to it, step by step. -->
          <ol class="m-0 flex list-none flex-col p-0">
            <li v-for="(step, index) in steps" :key="index" class="relative flex gap-3 pb-4 last:pb-0">
              <span aria-hidden="true" class="absolute top-7 bottom-0 left-[13px] w-px bg-line [li:last-child>&]:hidden" />
              <span :class="step.tone" class="relative flex size-7 flex-none items-center justify-center rounded-full border border-line bg-surface">
                <Icon :name="step.icon" :aria-hidden="true" class="text-sm" />
              </span>
              <div class="flex min-w-0 flex-1 flex-col gap-0.5 pt-1">
                <span class="flex flex-wrap items-baseline justify-between gap-x-3">
                  <span class="text-xs font-medium text-ink">{{ step.title }}</span>
                  <span v-if="step.at" class="text-[11px] text-ink/45">{{ when(step.at) }}</span>
                </span>
                <span class="text-xs text-ink/60">{{ step.detail }}</span>
              </div>
            </li>
          </ol>

          <FactGrid :columns="2" :facts="facts" label="About this purchase" />

          <div class="flex flex-col gap-2">
            <span class="flex flex-col gap-0.5">
              <span class="text-xs font-medium text-ink">The record</span>
              <span class="text-[11px] text-ink/55">As the purchases API returns it.</span>
            </span>
            <CodeBlock :code="code" :title="`${record.event_id}.json`" />
          </div>
        </div>
      </div>

      <footer class="flex justify-end border-t border-line px-5 py-3">
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
