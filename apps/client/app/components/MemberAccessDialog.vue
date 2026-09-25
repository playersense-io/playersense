<script setup lang="ts">
import type { MemberAccess, MemberRecord } from '~/composables/members'

// Which projects one member reaches. Narrowing it takes effect on their next request.
const props = defineProps<{
  member: MemberRecord
  save: (member: MemberRecord, access: MemberAccess) => Promise<void>
}>()

const open = defineModel<boolean>({ required: true })

const access = ref<MemberAccess>({ all_projects: true, projects: [] })
const saving = ref(false)
const problem = ref('')

watch([open, () => props.member.id], ([value]) => {
  if (!value) return
  access.value = { all_projects: props.member.all_projects, projects: (props.member.projects ?? []).map(project => project.slug) }
  problem.value = ''
}, { immediate: true })

const ready = computed(() => access.value.all_projects || access.value.projects.length > 0)

async function submit() {
  if (!ready.value || saving.value) return
  saving.value = true
  problem.value = ''
  try {
    await props.save(props.member, access.value)
    open.value = false
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <v-dialog v-model="open" max-width="440" scrollable>
    <v-card border flat>
      <form class="flex flex-col gap-5 overflow-y-auto p-5" @submit.prevent="submit">
        <header class="flex flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            Access for {{ member.name || member.email }}
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            What their role allows, in these projects only. A change applies from their next request.
          </p>
        </header>

        <ProjectAccessField v-model="access" />

        <p v-if="problem" class="m-0 text-xs text-error">
          {{ problem }}
        </p>

        <footer class="flex justify-end gap-2">
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
            :disabled="!ready"
            :loading="saving"
            class="text-xs font-medium"
            color="primary"
            size="small"
            type="submit"
            variant="flat"
          >
            Save access
          </v-btn>
        </footer>
      </form>
    </v-card>
  </v-dialog>
</template>
