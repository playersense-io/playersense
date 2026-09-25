<script setup lang="ts">
// A JSON document typed into a block that looks like CodeBlock and is highlighted as it is:
// the same Shiki theme, band, line numbers and colours, so a payload being written reads the
// way it will read once saved.
//
// The editing is a plain textarea laid exactly over the highlighted lines, its own text made
// transparent so only its caret and selection show. The two share one face, one size and one
// line height, and sit in one box that scrolls, so they never drift apart. Checked as it is
// typed; what does not parse yet is said under the block, and the last good value stays the
// model until it parses again.
const props = withDefaults(defineProps<{
  /** Names the field for a screen reader: "Payload". */
  label: string
  /** The band's label. */
  title?: string
  /** The fewest lines the box shows, empty or not. */
  minLines?: number
}>(), {
  title: 'JSON',
  minLines: 6
})

const value = defineModel<Json>({ required: true })
const invalid = defineModel<boolean>('invalid', { default: false })

const write = (json: Json) => JSON.stringify(json, null, 2)

const text = ref(write(value.value))
const problem = ref('')

/* Only a value that differs from what the box already means is written back into it, so the
   caret does not jump while a document is being typed. */
watch(value, (current) => {
  try {
    if (sameJson(JSON.parse(text.value), current)) return
  } catch {
    // The box holds something unparsed: the model changed from outside, so show the model.
  }
  text.value = write(current)
  problem.value = ''
  invalid.value = false
})

function onInput(next: string) {
  text.value = next
  try {
    value.value = JSON.parse(next) as Json
    problem.value = ''
    invalid.value = false
  } catch (error) {
    problem.value = next.trim() ? `Not valid JSON yet: ${(error as Error).message.replace(/^JSON\.parse: /, '')}` : 'Empty. Write null for a payload with nothing in it.'
    invalid.value = true
  }
}

function format() {
  if (!problem.value) text.value = write(value.value)
}

const html = ref('')

watchEffect(async () => {
  // A trailing newline still gets its line, so the caret on it has a number beside it.
  const code = text.value
  const highlighter = await getShikiHighlighter()
  if (!highlighter.getLoadedThemes().includes(PANEL_CODE_THEME.name)) await highlighter.loadTheme(PANEL_CODE_THEME)
  html.value = highlighter.codeToHtml(code, { lang: 'json', theme: PANEL_CODE_THEME.name })
})

const lines = computed(() => text.value.split('\n'))
const height = computed(() => `${Math.max(props.minLines, lines.value.length) * 20 + 24}px`)
</script>

<template>
  <div class="flex flex-col gap-1.5">
    <div
      :class="problem ? 'border-error/60' : 'border-line focus-within:border-primary'"
      class="ps-code-block ps-code-block--numbered ps-json-editor flex min-w-0 flex-col overflow-hidden rounded-(--ps-radius-large) border transition-colors duration-150"
    >
      <header class="flex h-9 flex-none items-center justify-between gap-2 border-b border-line bg-ink/3 pr-1.5 pl-3">
        <span class="text-[11px] font-medium text-ink/55">{{ title }}</span>
        <span class="flex items-center gap-0.5">
          <v-btn
            :disabled="Boolean(problem)"
            class="text-[11px]"
            color="neutral"
            density="comfortable"
            size="x-small"
            variant="text"
            @click="format"
          >
            Format
          </v-btn>
          <CopyButton :value="text" label="Copy the JSON" />
        </span>
      </header>
      <div class="max-h-96 min-h-0 overflow-auto">
        <div :style="{ minHeight: height }" class="relative w-max min-w-full">
          <div v-if="html" aria-hidden="true" :innerHTML="html" />
          <pre v-else aria-hidden="true"><code><span v-for="(line, index) in lines" :key="index" class="ps-code-line">{{ line }}
</span></code></pre>
          <textarea
            :aria-invalid="Boolean(problem)"
            :aria-label="label"
            :value="text"
            autocapitalize="off"
            autocomplete="off"
            class="ps-json-editor__input absolute inset-0 size-full resize-none overflow-hidden border-0 bg-transparent outline-none"
            spellcheck="false"
            wrap="off"
            @blur="format"
            @input="onInput(($event.target as HTMLTextAreaElement).value)"
          />
        </div>
      </div>
    </div>
    <span :class="problem ? 'text-error' : 'text-ink/50'" class="flex items-center gap-1.5 text-[11px]">
      <Icon :name="problem ? 'material-symbols:error-outline' : 'material-symbols:check-circle-outline'" :aria-hidden="true" class="text-sm" />
      {{ problem || 'Valid JSON. PlayerSense passes it through untouched; the game parses it.' }}
    </span>
  </div>
</template>

<style scoped>
/* The textarea's text sits exactly on the highlighted text: CodeBlock's padding, face, size and
   line height, and the gutter's width (16px, two digits, 16px) before the first character. */
.ps-json-editor__input {
  padding: 12px 16px 12px calc(32px + 2ch);
  color: transparent;
  caret-color: rgb(var(--v-theme-on-surface));
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 20px;
  tab-size: 2;
  white-space: pre;
}

.ps-json-editor__input::selection {
  background: rgba(var(--v-theme-primary), 0.22);
  color: transparent;
}
</style>
