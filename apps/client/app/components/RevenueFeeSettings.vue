<script setup lang="ts">
// How net revenue is estimated: the share each store keeps of what it sells, the organization's to
// set. A store's real rate cannot be known from here (Apple's Small Business Program and Google's
// first million dollars take less, and neither says which applies), so every net figure is an
// estimate, and this card says why beside the rates. A rate left alone follows the usual one.
const emit = defineEmits<{ saved: [] }>()

const { settings, loading, error, reload, save } = useRevenueSettings()

/* The rates being edited, in percent, by store; the ones on file until changed. */
const draft = ref<Record<string, number>>({})

watch(settings, (value) => {
  draft.value = Object.fromEntries((value?.stores ?? []).map(entry => [entry.store, entry.percent]))
}, { immediate: true })

const rows = computed(() => (settings.value?.stores ?? []).map(entry => ({ ...entry, value: draft.value[entry.store] ?? entry.percent })))

const changed = computed(() => rows.value.filter(row => row.value !== row.percent))
const invalid = computed(() => rows.value.some(row => !Number.isFinite(row.value) || row.value < 0 || row.value > 100))

const saving = ref(false)
const saved = ref(false)
const problem = ref('')

async function commit(fees: Record<string, number | null>) {
  saving.value = true
  problem.value = ''
  try {
    await save(fees)
    saved.value = true
    setTimeout(() => (saved.value = false), 2400)
    emit('saved')
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}

function apply() {
  if (invalid.value || !changed.value.length) return
  /* A rate set back to the usual one is sent as null, so it follows the usual rate from then on. */
  void commit(Object.fromEntries(changed.value.map(row => [row.store, row.value === row.default_percent ? null : row.value])))
}

function resetRate(store: string) {
  void commit({ [store]: null })
}

function setRate(store: string, text: string) {
  draft.value = { ...draft.value, [store]: Number(text) }
}

const ratesWords = computed(() => {
  const rates = settings.value?.rates
  if (!rates) return ''
  if (!rates.available) return 'No exchange rates on this deployment: revenue is shown as reported, and net cannot be worked out.'
  return `Converted to ${rates.base} with ${rates.currencies} currencies' rates${rates.as_of ? `, as of ${formatDateTime(rates.as_of)}` : ''}.`
})
</script>

<template>
  <SectionCard description="What each store is taken to keep of what it sells, for the organization's net revenue. Change a rate to match your agreement with the store." title="How net is estimated">
    <LoadState
      :error="error"
      :loading="loading"
      what="the store rates"
      @retry="reload"
    >
      <div v-if="settings" class="flex flex-col gap-4">
        <p class="m-0 flex items-start gap-2.5 rounded-(--ps-radius) bg-ink/4 px-3.5 py-3 text-xs leading-5 text-ink/75">
          <Icon aria-hidden="true" class="mt-0.5 flex-none text-base text-primary" name="material-symbols:info-outline" />
          <span>{{ settings.note }}</span>
        </p>

        <ul class="m-0 flex list-none flex-col divide-y divide-line p-0">
          <li v-for="row in rows" :key="row.store" class="flex flex-wrap items-center gap-x-5 gap-y-2 py-2.5">
            <span class="flex min-w-40 flex-1 items-center gap-2 text-sm text-ink">
              <PlatformIcon :platform="storeLook(row.store).platform" />
              {{ storeLook(row.store).label }}
              <span v-if="row.custom" class="rounded-full bg-primary/10 px-1.5 text-[10px] leading-4 text-primary">Set by you</span>
            </span>
            <span class="flex items-center gap-2">
              <v-text-field
                :aria-label="`${storeLook(row.store).label}'s share, in percent`"
                :model-value="String(row.value)"
                class="w-24 font-mono"
                density="compact"
                hide-details
                max="100"
                min="0"
                step="0.5"
                suffix="%"
                type="number"
                variant="outlined"
                @update:model-value="setRate(row.store, $event)"
              />
              <span class="w-28 text-[11px] text-ink/50">Usual {{ row.default_percent }}%</span>
              <v-btn
                :disabled="!row.custom || saving"
                class="text-xs"
                color="neutral"
                size="small"
                variant="text"
                @click="resetRate(row.store)"
              >
                Use usual
              </v-btn>
            </span>
          </li>
        </ul>

        <div class="flex flex-wrap items-center justify-end gap-2 border-t border-line pt-4">
          <span class="mr-auto text-[11px] text-ink/50">{{ ratesWords }}</span>
          <span v-if="saved" class="flex items-center gap-1 text-xs text-success">
            <Icon aria-hidden="true" class="text-sm" name="material-symbols:check" />
            Saved
          </span>
          <span v-else-if="problem" class="text-xs text-error">{{ problem }}</span>
          <span v-else-if="invalid" class="text-xs text-error">A share is a percent from 0 to 100</span>
          <v-btn
            :disabled="!changed.length || invalid"
            :loading="saving"
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="apply"
          >
            Save rates
          </v-btn>
        </div>
      </div>
    </LoadState>
  </SectionCard>
</template>
