<script setup lang="ts">
const platforms = [
  { name: 'Unity', status: 'Shipping', detail: 'iOS, iOS Simulator, Android (arm64, arm32, x86_64), Apple silicon macOS' },
  { name: 'Unity — Windows / Linux / Intel macOS', status: 'Not yet', detail: 'Blocked on CI runners, not code' },
  { name: 'Unreal', status: 'Not built', detail: '' },
  { name: 'Godot', status: 'Not built', detail: '' },
  { name: 'Native iOS / Android', status: 'Not built', detail: '' },
  { name: 'JavaScript / Web', status: 'Not built', detail: '' }
]
</script>

<template>
  <UContainer class="py-12">
    <UPageHeader
      title="SDK install guide"
      description="One protocol, implemented once in Rust, with a thin binding per platform."
    />

    <UPageSection title="Unity">
      <p class="text-muted mb-4">
        Add your project key in Project Settings, then:
      </p>
      <pre class="rounded-lg bg-elevated p-4 text-sm overflow-x-auto"><code>PlayerSense.Analytics.Track("level_completed", new() { ["level"] = 12 });
PlayerSense.Context.Player.Set("rank", "diamond");</code></pre>
      <p class="text-muted mt-4">
        No <code>await</code>, no callback, no result to check.
      </p>
    </UPageSection>

    <UPageSection title="Platform support">
      <UTable
        :data="platforms"
        :columns="[
          { accessorKey: 'name', header: 'Platform' },
          { accessorKey: 'status', header: 'Status' },
          { accessorKey: 'detail', header: 'Notes' }
        ]"
      />
    </UPageSection>
  </UContainer>
</template>
