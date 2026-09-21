<script setup lang="ts">
definePageMeta({ title: 'ClickHouse Health' })
</script>

<template>
  <PagePlaceholder
    title="ClickHouse Health"
    description="Per-cell ClickHouse status: replication, merges, and which partitions sit on which storage tier (hot vs. cold, §33)."
  />
</template>
