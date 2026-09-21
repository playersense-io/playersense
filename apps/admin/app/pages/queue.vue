<script setup lang="ts">
definePageMeta({ title: 'Queue Backlog' })
</script>

<template>
  <PagePlaceholder
    title="Queue Backlog"
    description="Pub/Sub backlog in production, or the local NDJSON spool in dev — how far analytics-writer and archive-writer are behind."
  />
</template>
