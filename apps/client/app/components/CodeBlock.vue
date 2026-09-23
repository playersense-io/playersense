<script setup lang="ts">
// A block of code in the panel's own colours: a compiled document, a snapshot, a call in C#.
// A band on top names the language and copies the code; the lines are numbered, and scroll
// sideways rather than wrap, since a wrapped line of JSON reads as two.
//
// Highlighted by Shiki with PANEL_CODE_THEME (utils/code-theme.ts), whose colours are the
// variables below, set from the theme's palette for light and dark. The highlighter loads on
// first use; until it has, the same text is shown plain, so the block never waits to be read.
const props = withDefaults(defineProps<{
  code: string
  lang?: 'json' | 'csharp' | 'shellscript'
  /** The band's label; the language's name when absent. */
  title?: string
  lineNumbers?: boolean
}>(), {
  lang: 'json',
  title: '',
  lineNumbers: true
})

const html = ref('')

watchEffect(async () => {
  const { code, lang } = props
  const highlighter = await getShikiHighlighter()
  if (!highlighter.getLoadedThemes().includes(PANEL_CODE_THEME.name)) await highlighter.loadTheme(PANEL_CODE_THEME)
  html.value = highlighter.codeToHtml(code, { lang, theme: PANEL_CODE_THEME.name })
})

const plain = computed(() => props.code.split('\n'))
</script>

<template>
  <div :class="lineNumbers ? 'ps-code-block--numbered' : ''" class="ps-code-block flex min-w-0 flex-col overflow-hidden rounded-(--ps-radius-large) border border-line">
    <header class="flex h-9 flex-none items-center justify-between gap-2 border-b border-line bg-ink/3 pr-1.5 pl-3">
      <span class="text-[11px] font-medium text-ink/55">{{ title || CODE_LANGUAGE_NAMES[lang] }}</span>
      <CopyButton :value="code" label="Copy the code" />
    </header>
    <div class="min-h-0 flex-1 overflow-auto">
      <div v-if="html" :innerHTML="html" />
      <pre v-else><code><span v-for="(line, index) in plain" :key="index" class="ps-code-line">{{ line }}
</span></code></pre>
    </div>
  </div>
</template>

<style>
/*
 * Unscoped: the markup inside is Shiki's. Every rule is under `.ps-code-block`.
 *
 * The palette: each colour is a chart slot mixed towards the ink, as `Tag` mixes it, because
 * three of the slots sit under 3:1 on white and this is 12px text.
 */
.ps-code-block {
  --ps-code-text: rgba(var(--v-theme-on-surface), 0.86);
  --ps-code-key: rgb(var(--v-theme-on-surface));
  --ps-code-punctuation: rgba(var(--v-theme-on-surface), 0.42);
  --ps-code-comment: rgba(var(--v-theme-on-surface), 0.42);
  --ps-code-string: color-mix(in srgb, rgb(var(--v-theme-chart-3)) 72%, rgb(var(--v-theme-on-surface)));
  --ps-code-number: color-mix(in srgb, rgb(var(--v-theme-chart-4)) 72%, rgb(var(--v-theme-on-surface)));
  --ps-code-type: color-mix(in srgb, rgb(var(--v-theme-chart-6)) 78%, rgb(var(--v-theme-on-surface)));
  --ps-code-keyword: color-mix(in srgb, rgb(var(--v-theme-primary)) 82%, rgb(var(--v-theme-on-surface)));
  --ps-code-function: color-mix(in srgb, rgb(var(--v-theme-chart-5)) 70%, rgb(var(--v-theme-on-surface)));
  --ps-code-variable: color-mix(in srgb, rgb(var(--v-theme-chart-2)) 72%, rgb(var(--v-theme-on-surface)));

  background: rgba(var(--v-theme-on-surface), 0.02);
}

.ps-code-block pre {
  width: max-content;
  min-width: 100%;
  margin: 0;
  padding: 12px 0;
  background: transparent !important;
  font-family: var(--font-mono);
  font-size: 12px;
  line-height: 20px;
  tab-size: 2;
}

.ps-code-block code {
  counter-reset: line;
  font-family: inherit;
}

/* Shiki's lines, and the plain ones shown until it has loaded. */
.ps-code-block .line,
.ps-code-block .ps-code-line {
  padding-inline: 16px;
}

/* A line's number, in the gutter's own quiet ink, never selected with the code. */
.ps-code-block--numbered .line::before,
.ps-code-block--numbered .ps-code-line::before {
  counter-increment: line;
  content: counter(line);
  display: inline-block;
  width: 2ch;
  margin-right: 16px;
  color: rgba(var(--v-theme-on-surface), 0.25);
  text-align: right;
  user-select: none;
}
</style>
