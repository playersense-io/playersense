<script setup lang="ts">
import type { NuxtError } from '#app'

const props = defineProps<{
  error: NuxtError
}>()

const notFound = computed(() => props.error.statusCode === 404)

useHead({
  title: () => (notFound.value ? 'Page not found' : 'Something went wrong'),
  htmlAttrs: {
    lang: 'en'
  }
})
</script>

<template>
  <v-app>
    <v-main class="flex items-center justify-center">
      <EmptyState
        :description="notFound ? 'There is no page at this address. It may have moved, or the link was mistyped.' : error.message"
        :icon="notFound ? 'material-symbols:explore-off-outline' : 'material-symbols:error-outline'"
        :title="notFound ? 'Page not found' : `Error ${error.statusCode}`"
      >
        <template #actions>
          <v-btn color="primary" variant="flat" @click="clearError({ redirect: '/' })">
            Back to the dashboard
          </v-btn>
        </template>
      </EmptyState>
    </v-main>
  </v-app>
</template>
