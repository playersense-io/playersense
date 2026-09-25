<script setup lang="ts">
// A new project, from the project menu: the same form Getting started asks the first one with.
const open = defineModel<boolean>({ required: true })

const form = ref<{ reset: () => void } | null>(null)

watch(open, (value) => {
  if (value) form.value?.reset()
})
</script>

<template>
  <v-dialog v-model="open" max-width="480" scrollable>
    <v-card border flat>
      <div class="flex flex-col gap-5 overflow-y-auto p-5">
        <header class="flex flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            New project
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            One project is one game. It gets a production, a staging and a development environment, each with its own keys.
          </p>
        </header>

        <ProjectForm
          ref="form"
          cancellable
          @cancel="open = false"
          @created="open = false"
        />
      </div>
    </v-card>
  </v-dialog>
</template>
