<script setup lang="ts">
// How much of a running test's audience is let in. Ramping up is the usual way to run a risky
// test: a few percent first, to catch anything broken, then more once it is safe. Raising the
// share keeps everybody already in, since who enters is decided by a hash of the install;
// lowering it takes players out mid-test, which the dialog says before it is done.
const props = defineProps<{
  experiment: ExperimentDef
}>()

const open = defineModel<boolean>({ required: true })

const emit = defineEmits<{ confirm: [traffic: number] }>()

/* In hundredths, as the share field and the slider hold it. */
const share = ref(props.experiment.traffic * 100)

const traffic = computed(() => Math.round(share.value / 100))
const lowering = computed(() => traffic.value < props.experiment.traffic)

function confirm() {
  emit('confirm', Math.max(1, traffic.value))
  open.value = false
}
</script>

<template>
  <v-dialog v-model="open" max-width="480">
    <v-card border flat>
      <div class="flex flex-col gap-5 p-5">
        <header class="flex flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            Traffic
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            The share of {{ experiment.audience.toLowerCase() }} let into this test. The rest keep what the game serves today and are not counted.
          </p>
        </header>

        <div class="flex items-center gap-3">
          <ShareField v-model="share" label="Traffic" />
          <v-slider
            :model-value="share / 100"
            aria-label="Traffic"
            class="min-w-0 flex-1"
            color="primary"
            hide-details
            max="100"
            min="1"
            step="1"
            thumb-size="14"
            track-size="4"
            @update:model-value="share = Math.round($event) * 100"
          />
        </div>

        <p v-if="lowering" class="m-0 flex items-start gap-2 rounded-(--ps-radius) bg-warning/8 px-3 py-2.5 text-xs leading-5 text-ink/80 ring-1 ring-warning/20 ring-inset">
          <Icon :aria-hidden="true" class="mt-0.5 flex-none text-sm text-warning" name="material-symbols:warning-outline" />
          Lowering it takes players out of a test they are already in. Their results so far stay, but they will stop seeing their variant.
        </p>
        <p v-else class="m-0 text-[11px] leading-4 text-ink/55">
          Raising it only adds players: everybody already in keeps their variant.
        </p>

        <footer class="flex items-center justify-end gap-2">
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
            :disabled="traffic === experiment.traffic"
            class="text-xs font-medium"
            color="primary"
            size="small"
            variant="flat"
            @click="confirm"
          >
            Set traffic to {{ traffic }}%
          </v-btn>
        </footer>
      </div>
    </v-card>
  </v-dialog>
</template>
