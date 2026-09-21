<script setup lang="ts">
definePageMeta({ title: 'Exports' })
</script>

<template>
  <PagePlaceholder
    title="Exports"
    description="Download everything held about one player."
    blocked="The existing /v1/privacy/exports route needs a server key — not reachable from a signed-in dashboard session yet."
  />
</template>
