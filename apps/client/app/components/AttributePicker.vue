<script setup lang="ts">
// The key a condition reads, chosen from a menu: every key PlayerSense knows, grouped by what
// it describes, each with who asserts it, and the game's own context keys after them. A key of
// the game's own that is not listed yet can be typed in — "rank" becomes `context.rank` — since
// a studio's vocabulary is its own (docs/sdk/context-and-targeting.md §1).
defineProps<{
  /** What the choice is, for a screen reader: "Condition 2 key". */
  label: string
}>()

const key = defineModel<string>({ required: true })

const emit = defineEmits<{
  /** A key typed in rather than picked: the row asks what its values are. */
  custom: [key: string]
}>()

const open = ref(false)
const search = ref<string | null>('')

const chosen = computed(() => (key.value ? attributeOf(key.value) : null))

const needle = computed(() => (search.value ?? '').trim().toLowerCase())

const groups = computed(() => {
  const found = new Map<string, AttributeDef[]>()
  for (const attribute of ATTRIBUTES) {
    if (needle.value && !attribute.key.toLowerCase().includes(needle.value) && !attribute.label.toLowerCase().includes(needle.value)) continue
    const group = groupOfKey(attribute.key)
    found.set(group, [...(found.get(group) ?? []), attribute])
  }
  return [...found.entries()]
})

/** What typing would create: the text as a context key, when it is a key and not one listed. */
const customKey = computed(() => {
  const text = needle.value.replace(/^context\./, '')
  if (!text || !/^[a-z0-9][a-z0-9_.-]*$/i.test(text)) return null
  const full = `context.${text}`
  return ATTRIBUTES.some(attribute => attribute.key === full) ? null : full
})

function choose(next: string, custom = false) {
  key.value = next
  open.value = false
  if (custom) emit('custom', next)
}

function focusSearch() {
  document.querySelector<HTMLInputElement>('.v-overlay--active .ps-attribute-picker__search input')?.focus()
}
</script>

<template>
  <v-menu
    v-model="open"
    :close-on-content-click="false"
    :offset="4"
    location="bottom start"
    @after-enter="focusSearch"
    @update:model-value="search = ''"
  >
    <template #activator="{ props: activator }">
      <button
        v-bind="activator"
        :aria-label="`${label}: ${chosen?.label ?? 'none chosen'}`"
        :class="chosen ? 'text-ink' : 'text-primary'"
        class="inline-flex h-8 max-w-full min-w-0 items-center gap-1.5 rounded-(--ps-radius-small) border border-line bg-surface px-2 text-xs transition-colors duration-150 hover:border-ink/30 focus-visible:outline-2 focus-visible:outline-primary"
        type="button"
      >
        <template v-if="chosen">
          <AuthorityMark :authority="chosen.authority" />
          <span class="truncate font-medium">{{ chosen.label }}</span>
          <span class="hidden truncate font-mono text-[11px] text-ink/45 sm:inline">{{ chosen.key }}</span>
        </template>
        <template v-else>
          <Icon aria-hidden="true" class="text-sm" name="material-symbols:add" />
          Choose a key
        </template>
        <Icon aria-hidden="true" class="flex-none text-sm text-ink/40" name="material-symbols:unfold-more" />
      </button>
    </template>

    <v-card class="w-96 max-w-[calc(100vw-2rem)]" flat>
      <div class="border-b border-line p-2">
        <v-text-field
          v-model="search"
          aria-label="Find a key"
          class="ps-attribute-picker__search"
          clearable
          density="comfortable"
          hide-details
          placeholder="Find a key, or type one of the game's own"
          prepend-inner-icon="material-symbols:search"
          variant="filled"
          @keydown.enter.prevent="customKey && choose(customKey, true)"
        />
      </div>
      <div class="max-h-96 overflow-y-auto p-1">
        <button
          v-if="customKey"
          class="flex w-full items-center gap-2 rounded-(--ps-radius-x-small) px-2 py-2 text-left hover:bg-ink/4"
          type="button"
          @click="choose(customKey, true)"
        >
          <Icon aria-hidden="true" class="text-base text-primary" name="material-symbols:add" />
          <span class="flex min-w-0 flex-col">
            <span class="text-xs text-ink">Use <span class="font-mono">{{ customKey }}</span></span>
            <span class="text-[11px] text-ink/55">A key the game sets with Context.Set</span>
          </span>
        </button>
        <template v-for="[group, attributes] in groups" :key="group">
          <p class="m-0 px-2 pt-2 pb-1 text-[11px] text-ink/45">
            {{ group }}
          </p>
          <button
            v-for="attribute in attributes"
            :key="attribute.key"
            :class="attribute.key === key ? 'bg-primary/8' : 'hover:bg-ink/4'"
            class="flex w-full items-start gap-2 rounded-(--ps-radius-x-small) px-2 py-1.5 text-left transition-colors duration-150"
            type="button"
            @click="choose(attribute.key)"
          >
            <span class="mt-0.5"><AuthorityMark :authority="attribute.authority" /></span>
            <span class="flex min-w-0 flex-1 flex-col gap-0.5">
              <span class="flex items-baseline justify-between gap-2">
                <span class="truncate text-xs text-ink">{{ attribute.label }}</span>
                <span class="flex-none font-mono text-[10px] text-ink/40">{{ attribute.key }}</span>
              </span>
              <span class="line-clamp-2 text-[11px] leading-4 text-ink/55">{{ attribute.note }}</span>
            </span>
          </button>
        </template>
        <p v-if="!groups.length && !customKey" class="m-0 px-2 py-3 text-xs text-ink/55">
          No key matches. The game's own keys are letters, digits, dots, dashes and underscores.
        </p>
      </div>
    </v-card>
  </v-menu>
</template>
