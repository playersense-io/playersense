<script setup lang="ts">
// "Go to": every screen in the navigation tree, filtered as you type, opened with ⌘K /
// Ctrl+K. Arrow keys move, Enter goes. Keyboard only — the bar has no button for it.
const navigation = useNavigation()
const router = useRouter()

const open = ref(false)
const query = ref('')
const highlighted = ref(0)

const results = computed(() => {
  const needle = query.value.trim().toLowerCase()
  return navigation.links
    .map(link => ({ ...link, group: navigation.groupOf(link.to)?.title }))
    .filter(link => !needle || `${link.group ?? ''} ${link.title}`.toLowerCase().includes(needle))
})

watch(query, () => {
  highlighted.value = 0
})

useEventListener('keydown', (event: KeyboardEvent) => {
  if (event.key.toLowerCase() === 'k' && (event.metaKey || event.ctrlKey)) {
    event.preventDefault()
    toggle()
  }
})

function toggle() {
  query.value = ''
  open.value = !open.value
}

function move(step: number) {
  const count = results.value.length
  if (count) highlighted.value = (highlighted.value + step + count) % count
}

async function go(to?: string) {
  const target = to ?? results.value[highlighted.value]?.to
  if (!target) return
  open.value = false
  await nextTick()
  await router.push(target)
}
</script>

<template>
  <!-- Mounted only while open: a Vuetify dialog closed in the same flush as a navigation
       leaves its teleported content painted with its model already false. -->
  <v-dialog
    v-if="open"
    v-model="open"
    max-width="520"
    width="100%"
    location="top"
    class="mt-16"
  >
    <v-card>
      <div class="border-b border-line p-3">
        <v-text-field
          v-model="query"
          aria-label="Screen name"
          autofocus
          density="comfortable"
          hide-details
          placeholder="Go to…"
          prepend-inner-icon="material-symbols:search"
          variant="outlined"
          @keydown.down.prevent="move(1)"
          @keydown.up.prevent="move(-1)"
          @keydown.enter.prevent="go()"
        />
      </div>

      <v-list v-if="results.length" class="max-h-96 overflow-y-auto" nav>
        <v-list-item
          v-for="(result, index) in results"
          :key="result.to"
          :active="index === highlighted"
          :prepend-icon="result.icon ?? navigation.groupOf(result.to)?.icon"
          :subtitle="result.group"
          :title="result.title"
          @click="go(result.to)"
          @mouseenter="highlighted = index"
        />
      </v-list>
      <p v-else class="m-0 px-4 py-8 text-center text-sm text-ink/55">
        No screen matches “{{ query }}”.
      </p>
    </v-card>
  </v-dialog>
</template>
