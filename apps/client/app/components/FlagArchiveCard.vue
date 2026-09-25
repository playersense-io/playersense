<script setup lang="ts">
// The last thing on a flag's settings: taking it out of the configuration, or putting it back.
//
// Archiving is safe to undo and easy to get wrong, so the card says what it would do to this
// flag in particular before the button: which environments serve it now and keep serving it
// until their next publish, whether production devices are still reading it (in which case the
// game's code is the thing to change first), and that nothing is deleted. Once archived, the
// same place offers the way back.
const props = defineProps<{
  flag: FlagDef
}>()

const emit = defineEmits<{
  archive: []
  restore: []
}>()

const { latest } = useLiveConfig()

const servedIn = computed(() => ENVIRONMENTS.filter(environment => latest(environment)?.flags.some(entry => entry.key === props.flag.key)))

const { readsOf } = useFlagExposures()
const reads = computed(() => (servedIn.value.includes('production') ? readsOf(props.flag.key).reads : 0))

function list(names: string[]): string {
  if (names.length <= 1) return names.join('')
  return `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`
}
</script>

<template>
  <section
    v-if="!flag.archived"
    aria-labelledby="flag-archive-title"
    class="flex flex-col gap-5 rounded-(--ps-radius-x-large) border border-error/25 bg-error/3 p-5"
  >
    <header class="flex items-start gap-3">
      <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-error/10 text-error">
        <Icon class="text-lg" name="material-symbols:archive-outline" />
      </span>
      <div class="flex min-w-0 flex-col gap-0.5">
        <h2 id="flag-archive-title" class="m-0 text-sm font-medium text-ink">
          Archive this flag
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          It leaves the draft and every publish after this one. Here is what that means for {{ flag.name }}.
        </p>
      </div>
    </header>

    <ul class="m-0 flex list-none flex-col gap-3 p-0">
      <li class="flex items-start gap-2.5 text-xs leading-5 text-ink/75">
        <Icon :aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:deployed-code-outline" />
        <span v-if="servedIn.length">
          {{ list(servedIn.map(environment => ENVIRONMENT_LABELS[environment])) }} {{ servedIn.length === 1 ? 'serves' : 'serve' }} it now,
          and keep serving it until the next publish there. After that, devices use the value written in the game's code.
        </span>
        <span v-else>No environment serves it yet, so archiving it changes nothing any player gets.</span>
      </li>

      <li
        :class="reads ? 'text-ink/80' : 'text-ink/75'"
        class="flex items-start gap-2.5 text-xs leading-5"
      >
        <Icon
          :class="reads ? 'text-warning' : 'text-success'"
          :name="reads ? 'material-symbols:warning-outline' : 'material-symbols:check-circle-outline'"
          :aria-hidden="true"
          class="mt-0.5 flex-none text-base"
        />
        <span v-if="reads">
          Production devices read it <span class="font-mono text-ink">{{ writeFigure(reads, { compact: true }) }}</span> times last week.
          Remove the check from the game's code first, or those players fall back to whatever the code says.
        </span>
        <span v-else>No production device read it last week, so no player depends on it.</span>
      </li>

      <li class="flex items-start gap-2.5 text-xs leading-5 text-ink/75">
        <Icon :aria-hidden="true" class="mt-0.5 flex-none text-base text-ink/45" name="material-symbols:history" />
        <span>
          Nothing is deleted. Its rules and history stay, it can be restored at any time, and its key
          <span class="font-mono text-ink">{{ flag.key }}</span> is never given to another flag.
        </span>
      </li>
    </ul>

    <footer class="flex flex-wrap items-center justify-between gap-3 border-t border-error/15 pt-4">
      <span class="text-[11px] text-ink/50">You will be asked to confirm.</span>
      <v-btn
        class="text-xs font-medium"
        color="error"
        prepend-icon="material-symbols:archive-outline"
        size="small"
        variant="flat"
        @click="emit('archive')"
      >
        Archive flag
      </v-btn>
    </footer>
  </section>

  <section
    v-else
    aria-labelledby="flag-restore-title"
    class="flex flex-wrap items-center justify-between gap-4 rounded-(--ps-radius-x-large) border border-line bg-surface p-5"
  >
    <div class="flex min-w-0 items-start gap-3">
      <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-ink/6 text-ink/60">
        <Icon class="text-lg" name="material-symbols:unarchive-outline" />
      </span>
      <div class="flex min-w-0 flex-col gap-0.5">
        <h2 id="flag-restore-title" class="m-0 text-sm font-medium text-ink">
          This flag is archived
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          Restoring it puts it back in the draft with its rules as they were. Players get it again once the draft is published.
        </p>
      </div>
    </div>
    <v-btn
      class="text-xs font-medium"
      color="primary"
      prepend-icon="material-symbols:unarchive-outline"
      size="small"
      variant="flat"
      @click="emit('restore')"
    >
      Restore flag
    </v-btn>
  </section>
</template>
