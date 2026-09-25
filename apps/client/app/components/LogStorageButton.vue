<script setup lang="ts">
import type { Schema } from '@playersense/contracts'

/**
 * What the project keeps and for how long, on one button in a Cloud Logging page's header, and the
 * storage dialog behind it. On both pages, because retention is asked about from either.
 */
const workspace = useWorkspaceStore()
const path = computed(() => (workspace.projectPath ? `${workspace.projectPath}/log-storage` : null))

const open = ref(false)
const read = useApiGet<Schema<'LogStorageOut'>>(() => path.value)
const storage = computed(() => read.data.value)

/* Decimal, as the bill counts. */
const kept = computed(() => {
  const bytes = storage.value?.kept_bytes ?? 0
  return bytes < 1e7 ? `${(bytes / 1e6).toFixed(1)} MB` : `${(bytes / 1e9).toFixed(2)} GB`
})
</script>

<template>
  <v-btn
    class="text-xs font-medium"
    prepend-icon="material-symbols:database-outline"
    size="small"
    variant="outlined"
    @click="open = true"
  >
    <span class="flex items-center gap-1.5">
      {{ kept }}
      <span class="text-ink/45">·</span>
      {{ storage ? RETENTION_WORDS[storage.retention_days] ?? `${storage.retention_days} days` : '…' }}
    </span>
  </v-btn>
  <LogStorageDialog v-model="open" :path="path" @changed="read.execute()" />
</template>
