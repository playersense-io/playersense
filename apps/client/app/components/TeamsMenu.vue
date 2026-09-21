<script setup lang="ts">
import type { DropdownMenuItem } from '@nuxt/ui'

defineProps<{
  collapsed?: boolean
}>()

// Placeholder until control-api's /organizations + project-access list are
// wired in — this becomes the real organization/project switcher (§55).
const organizations = ref([{
  label: 'Acme Games',
  avatar: { text: 'AG' }
}])
const selectedOrganization = ref(organizations.value[0])

const items = computed<DropdownMenuItem[][]>(() => {
  return [organizations.value.map(organization => ({
    ...organization,
    onSelect() {
      selectedOrganization.value = organization
    }
  })), [{
    label: 'Switch organization',
    icon: 'i-lucide-repeat'
  }]]
})
</script>

<template>
  <UDropdownMenu
    :items="items"
    :content="{ align: 'center', collisionPadding: 12 }"
    :ui="{ content: collapsed ? 'w-40' : 'w-(--reka-dropdown-menu-trigger-width)' }"
  >
    <UButton
      v-bind="{
        ...selectedOrganization,
        label: collapsed ? undefined : selectedOrganization?.label,
        trailingIcon: collapsed ? undefined : 'i-lucide-chevrons-up-down'
      }"
      color="neutral"
      variant="ghost"
      block
      :square="collapsed"
      class="data-[state=open]:bg-elevated"
      :class="[!collapsed && 'py-2']"
      :ui="{
        trailingIcon: 'text-dimmed'
      }"
    />
  </UDropdownMenu>
</template>
