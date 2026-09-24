<script setup lang="ts">
// One purchase, in full: what was bought, by whom, where and for how much, and what happened to
// it step by step (bought, checked with the store, granted or turned away, refunded), then
// every identifier somebody would quote to a store's support, and the record itself as the
// API and the purchase webhook will hand it to the studio's own backend.
const props = defineProps<{
  record: PurchaseRecord | null
}>()

const open = defineModel<boolean>({ required: true })

const product = computed(() => PRODUCTS.find(entry => entry.id === props.record?.productId))

const when = (at: number) => new Date(at).toLocaleString('en', { weekday: 'short', month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false })

interface Step {
  icon: string
  tone: string
  title: string
  detail: string
  at?: number
}

/* What happened to it, in order. */
const steps = computed<Step[]>(() => {
  const record = props.record
  if (!record) return []
  const store = STORES[record.store].label
  const out: Step[] = [
    { icon: 'material-symbols:shopping-cart-outline', tone: 'text-ink/60', title: `Bought in ${store}`, detail: `${product.value?.name ?? record.productId} for ${writeLocal(record.local, record.currency)}`, at: record.at }
  ]
  if (record.status === 'rejected') {
    out.push({ icon: 'material-symbols:gpp-bad-outline', tone: 'text-error', title: 'Turned away', detail: `${record.rejection ?? 'The receipt did not check out'}. Nothing was granted and nothing counted.`, at: record.at + record.verifyMs })
    return out
  }
  out.push({ icon: 'material-symbols:verified-outline', tone: 'text-success', title: `Verified with ${store}`, detail: `The store confirmed it in ${record.verifyMs} ms.`, at: record.at + record.verifyMs })
  if (record.status === 'sandbox') {
    out.push({ icon: 'material-symbols:science-outline', tone: 'text-ink/60', title: 'A test purchase', detail: 'Made by a tester account: granted in the game, kept out of revenue.' })
    return out
  }
  out.push({ icon: 'material-symbols:redeem-outline', tone: 'text-primary', title: 'Counted as revenue', detail: `${formatExactMoney(record.usd)} at the day's rate${record.first ? ', and the player\'s first purchase ever' : ''}.` })
  if (record.status === 'refunded' && record.refundedAt) {
    out.push({ icon: 'material-symbols:undo', tone: 'text-warning', title: 'Refunded', detail: `${record.refundReason ?? 'Refunded'}, ${Math.max(1, Math.round((record.refundedAt - record.at) / 3_600_000))} hours after it was bought.`, at: record.refundedAt })
  }
  return out
})

const facts = computed<Fact[]>(() => {
  const record = props.record
  if (!record) return []
  return [
    { key: 'player', label: 'Player', value: record.playerId, mono: true, to: `/analytics/players/${encodeURIComponent(record.playerId)}`, copy: record.playerId },
    { key: 'country', label: 'Country', value: segmentName('install_country', record.country) },
    { key: 'store', label: 'Store', value: STORES[record.store].label },
    { key: 'package', label: 'Package', value: `${product.value?.name ?? record.productId} (${record.productId})` },
    { key: 'paid', label: 'Paid', value: `${writeLocal(record.local, record.currency)} · ${formatExactMoney(record.usd)}` },
    { key: 'transaction', label: 'Store transaction', value: record.storeTransactionId, mono: true, copy: record.storeTransactionId, info: 'The store\'s own id. Quote it to the store\'s support; a retry with it is recognised and never granted twice.' },
    { key: 'id', label: 'Purchase id', value: record.id, mono: true, copy: record.id }
  ]
})

const code = computed(() => (props.record ? JSON.stringify(purchaseDocument(props.record, product.value), null, 2) : ''))
</script>

<template>
  <v-dialog v-model="open" max-width="720" scrollable>
    <v-card v-if="record" border flat>
      <header class="flex items-start justify-between gap-4 border-b border-line p-5">
        <div class="flex min-w-0 flex-col gap-1">
          <span class="flex items-center gap-2">
            <h2 class="m-0 truncate text-sm font-medium text-ink">
              {{ product?.name ?? record.productId }}
            </h2>
            <StatusBadge :label="PURCHASE_STATUS[record.status].label" :tone="PURCHASE_STATUS[record.status].tone" />
          </span>
          <span class="text-xs text-ink/55">{{ PURCHASE_STATUS[record.status].note }}</span>
        </div>
        <div class="flex flex-none flex-col items-end">
          <span class="font-mono text-lg text-ink">{{ writeLocal(record.local, record.currency) }}</span>
          <span v-if="record.currency !== 'USD'" class="font-mono text-[11px] text-ink/45">{{ formatExactMoney(record.usd) }}</span>
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
                <Icon :name="step.icon" aria-hidden="true" class="text-sm" />
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
              <span class="text-[11px] text-ink/55">What your backend receives for it, from the purchases API and the purchase webhook.</span>
            </span>
            <CodeBlock :code="code" :title="`${record.id}.json`" />
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
