<script setup lang="ts">
// How the game reads this flag, for the developer wiring it in: the Unity call for its kind,
// with the fallback written in, and the same flag over plain HTTP for an engine with no SDK.
//
// The value in the call is the game's own default — what a player gets on a first launch with
// no network, before any document has reached the device. It is not the flag's fallback, which
// is served by PlayerSense; the note says so, since keeping the two equal is the studio's job.
const props = defineProps<{
  flag: FlagDef
}>()

type Engine = 'unity' | 'http'
const engine = ref<Engine>('unity')
const ENGINES: SegmentedOption[] = [
  { value: 'unity', label: 'Unity' },
  { value: 'http', label: 'HTTP' }
]

const csharpLiteral = (value: Json) => {
  if (typeof value === 'string') return JSON.stringify(value)
  if (typeof value === 'boolean' || typeof value === 'number') return String(value)
  return JSON.stringify(JSON.stringify(value))
}

const identifier = computed(() => {
  const parts = props.flag.key.split(/[._-]+/).filter(Boolean)
  return parts.map((part, index) => (index ? part[0]!.toUpperCase() + part.slice(1) : part)).join('')
})

const unity = computed(() => {
  const { key, kind, fallback } = props.flag
  const name = identifier.value
  if (kind === 'bool') {
    return `// Records an exposure the first time it is read in a session.
bool ${name} = PlayerSense.Flags.Bool("${key}", ${csharpLiteral(fallback)});

if (${name})
{
    // …
}`
  }
  if (kind === 'number') {
    const whole = typeof fallback === 'number' && Number.isInteger(fallback)
    return `// Records an exposure the first time it is read in a session.
${whole ? 'int' : 'double'} ${name} = PlayerSense.Flags.${whole ? 'Int' : 'Number'}("${key}", ${csharpLiteral(fallback)});`
  }
  if (kind === 'string') {
    return `// Records an exposure the first time it is read in a session.
string ${name} = PlayerSense.Flags.Text("${key}", ${csharpLiteral(fallback)});`
  }
  const type = `${name[0]!.toUpperCase()}${name.slice(1)}`.replace(/(Config)?$/, 'Config')
  return `// A JSON flag arrives as text; the game parses it into its own type.
string json = PlayerSense.Flags.Text("${key}", ${csharpLiteral(fallback)});
${type} ${name} = JsonUtility.FromJson<${type}>(json);`
})

const http = computed(() => `curl -X POST "$PLAYERSENSE_CONFIG_ENDPOINT/v1/config" \\
  -H "Authorization: Bearer $PLAYERSENSE_PROJECT_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{
    "schema_version": 1,
    "install_id": "5f0c…",
    "app": { "version": "2.5.1" },
    "device": { "platform": "android" },
    "sdk": { "name": "custom", "version": "1.0.0" },
    "context": { "progress.level": 12 }
  }'

# → { "version": 38, "flags": { "${props.flag.key}": … }, "server_time_ms": … }`)

const code = computed(() => (engine.value === 'unity' ? unity.value : http.value))
</script>

<template>
  <div class="flex flex-col gap-3">
    <div class="flex items-center justify-between gap-2">
      <SegmentedControl v-model="engine" :options="ENGINES" label="Engine" />
      <CopyButton :value="code" label="Copy the code" />
    </div>
    <CodeBlock :code="code" :lang="engine === 'unity' ? 'csharp' : 'shellscript'" />
    <p class="m-0 text-[11px] leading-4 text-ink/55">
      <template v-if="engine === 'unity'">
        The value in the call is the game's own default: a first launch offline, before any configuration has
        arrived. Keep it equal to the flag's fallback, <FlagValue :value="flag.fallback" />, or those players see
        something nobody chose. Reads are dictionary lookups, safe to call every frame.
      </template>
      <template v-else>
        For an engine without an SDK. The key's environment decides which published version answers, and the
        response carries every flag's value and none of its rules. Send If-None-Match with the last ETag to get a 304.
      </template>
    </p>
  </div>
</template>
