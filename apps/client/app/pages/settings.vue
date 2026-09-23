<script setup lang="ts">
// The settings shell: the rail on the left, the tab it opened on the right. A parent
// route rather than a `?tab=`, so every tab is a URL somebody can be linked to and the
// breadcrumb can say "Settings / Members". No page header here: each tab owns its own.
//
// The scope on each row is the point as much as the title — project settings, the
// organization's team and its money sit side by side, and a change on the wrong one is
// invisible until afterwards.
const TABS: readonly SettingsTab[] = [
  { to: '/settings', title: 'General', scope: 'This project', icon: 'material-symbols:tune' },
  { to: '/settings/data-retention', title: 'Data retention', scope: 'This project', icon: 'material-symbols:database-outline' },
  { to: '/settings/members', title: 'Members', scope: 'Organization', icon: 'material-symbols:group-outline' },
  { to: '/settings/api-keys', title: 'API keys', scope: 'This project', icon: 'material-symbols:vpn-key-outline' },
  { to: '/settings/billing', title: 'Billing', scope: 'Organization', icon: 'material-symbols:receipt-long-outline' },
  { to: '/settings/audit-log', title: 'Audit log', scope: 'Organization', icon: 'material-symbols:history' }
]
</script>

<template>
  <!-- Stacked under `lg` rather than a rail squeezed to nothing. -->
  <div class="grid gap-6 lg:grid-cols-[196px_minmax(0,1fr)] lg:items-start lg:gap-8">
    <!-- Sticky under the 56px app bar, so the rail is reachable from the foot of a long tab. -->
    <div class="lg:sticky lg:top-20">
      <SettingsTabs :tabs="TABS" label="Settings" />
    </div>

    <div class="min-w-0">
      <NuxtPage />
    </div>
  </div>
</template>
