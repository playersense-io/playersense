<script setup lang="ts">
import type { MemberAccess, RoleRecord } from '~/composables/members'

// Inviting somebody: their address, the role accepting gives them and the projects it reaches.
// The invitation is accepted by signing in with that address; no mail provider is set up yet,
// so when the message was not sent the dialog says to tell them.
const props = defineProps<{
  roles: RoleRecord[]
  invite: (email: string, role: string, access: MemberAccess) => Promise<{ delivered: boolean, expires_at: string }>
}>()

const open = defineModel<boolean>({ required: true })

const email = ref('')
const role = ref('developer')
const access = ref<MemberAccess>({ all_projects: true, projects: [] })
const saving = ref(false)
const problem = ref('')
/** Set once sent: whether the message went out. */
const sent = ref<{ email: string, delivered: boolean, expires_at: string } | null>(null)

const ordered = computed(() => sortRoles(props.roles).filter(entry => entry.key !== 'owner'))
const ROLE_ITEMS = computed(() => ordered.value.map(entry => ({ value: entry.key, title: roleLook(entry).label, note: roleLook(entry).note })))

watch(open, (value) => {
  if (!value) return
  email.value = ''
  role.value = ordered.value.some(entry => entry.key === 'developer') ? 'developer' : (ordered.value[0]?.key ?? '')
  access.value = { all_projects: true, projects: [] }
  problem.value = ''
  sent.value = null
}, { immediate: true })

const ready = computed(() => /^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.value.trim()) && Boolean(role.value)
  && (access.value.all_projects || access.value.projects.length > 0))

async function submit() {
  if (!ready.value || saving.value) return
  saving.value = true
  problem.value = ''
  try {
    sent.value = { email: email.value.trim(), ...await props.invite(email.value.trim(), role.value, access.value) }
  } catch (caught) {
    problem.value = caught instanceof Error ? caught.message : String(caught)
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <v-dialog v-model="open" max-width="480" scrollable>
    <v-card border flat>
      <div v-if="sent" class="flex flex-col items-center gap-3 p-6 text-center">
        <Icon class="text-3xl text-success" name="material-symbols:check-circle-outline" />
        <h2 class="m-0 text-sm font-medium text-ink">
          {{ sent.email }} is invited
        </h2>
        <p class="m-0 text-xs leading-5 text-ink/60">
          <template v-if="sent.delivered">
            An email is on its way. They join by signing in with that address.
          </template>
          <template v-else>
            No email was sent. Tell them to sign in to PlayerSense with {{ sent.email }}: the invitation waits for them there until {{ formatDate(sent.expires_at) }}.
          </template>
        </p>
        <v-btn
          class="text-xs font-medium"
          color="primary"
          size="small"
          variant="flat"
          @click="open = false"
        >
          Done
        </v-btn>
      </div>

      <form v-else class="flex flex-col gap-5 overflow-y-auto p-5" @submit.prevent="submit">
        <header class="flex flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            Invite people
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            They join by signing in with the address invited. Up to 3 invitations per address, and 30 per organization, an hour.
          </p>
        </header>

        <div class="flex flex-col gap-1">
          <label class="text-xs font-medium text-ink" for="invite-email">Email</label>
          <v-text-field
            id="invite-email"
            v-model="email"
            autocomplete="off"
            autofocus
            hide-details
            placeholder="name@studio.com"
            type="email"
            variant="filled"
          />
        </div>

        <div class="flex flex-col gap-1">
          <label class="text-xs font-medium text-ink" for="invite-role">Role</label>
          <v-select
            id="invite-role"
            v-model="role"
            :items="ROLE_ITEMS"
            hide-details
            variant="filled"
          >
            <template #item="{ props: item, item: entry }">
              <v-list-item v-bind="item" :subtitle="entry.note" lines="two" />
            </template>
          </v-select>
        </div>

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
            Send invitation
          </v-btn>
        </footer>
      </form>
    </v-card>
  </v-dialog>
</template>
