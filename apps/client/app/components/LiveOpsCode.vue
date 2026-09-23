<script setup lang="ts">
// How the game reads this event, for the developer wiring it in: the Unity calls
// (sdk/unity/Runtime/LiveOps/LiveOpsModule.cs) with a type shaped like this event's payload, and
// the same answer over plain HTTP for an engine with no SDK.
//
// The game never evaluates the schedule. The server sends the window a player is in, and the
// next one, in its own clock, so "every Friday" becoming "every Saturday" is not a release.
const props = defineProps<{
  event: LiveOpsEventDef
}>()

type Engine = 'unity' | 'http'
const engine = ref<Engine>('unity')
const ENGINES: SegmentedOption[] = [
  { value: 'unity', label: 'Unity' },
  { value: 'http', label: 'HTTP' }
]

const pascal = (text: string) => text.split(/[._-]+/).filter(Boolean).map(part => part[0]!.toUpperCase() + part.slice(1)).join('')

/* C# fields for a payload object, a line each, typed from the published values. */
function fieldsOf(value: Json): string[] {
  if (!value || typeof value !== 'object' || Array.isArray(value)) return []
  return Object.entries(value).map(([key, entry]) => {
    const type = typeof entry === 'boolean'
      ? 'bool'
      : typeof entry === 'number'
        ? (Number.isInteger(entry) ? 'int' : 'float')
        : typeof entry === 'string'
          ? 'string'
          : Array.isArray(entry)
            ? (entry.every(item => typeof item === 'string') ? 'string[]' : entry.every(item => typeof item === 'number') ? 'int[]' : `${pascal(key)}Entry[]`)
            : pascal(key)
    return `    public ${type} ${key};`
  })
}

const unity = computed(() => {
  const { key, payload } = props.event
  const type = `${pascal(key)}Payload`
  const variable = pascal(key).replace(/^./, first => first.toLowerCase())
  const scalar = payload === null || typeof payload !== 'object'
  if (scalar) {
    const parse = typeof payload === 'number' ? 'float.Parse(running.Payload, System.Globalization.CultureInfo.InvariantCulture)' : typeof payload === 'boolean' ? 'running.Payload == "true"' : 'running.Payload.Trim(\'"\')'
    return `// The payload is published as-is: here, ${JSON.stringify(payload)}.
if (PlayerSense.LiveOps.TryGet("${key}", out var running))
{
    var ${variable} = ${parse};
    var left = PlayerSense.LiveOps.Remaining(running); // for a countdown
}`
  }
  return `[System.Serializable]
public class ${type}
{
${fieldsOf(payload).join('\n') || '    // Your payload\'s fields.'}
}

// Read when a screen opens, not every frame. Empty until the server has answered.
if (PlayerSense.LiveOps.TryGet("${key}", out var running))
{
    var ${variable} = JsonUtility.FromJson<${type}>(running.Payload);
    var left = PlayerSense.LiveOps.Remaining(running); // server clock, not the device's

    // running.NextStartsAtMs is when it comes back, or 0 when it never does.
}`
})

const http = computed(() => `curl -X POST "$PLAYERSENSE_CONFIG_ENDPOINT/v1/config" \\
  -H "Authorization: Bearer $PLAYERSENSE_PROJECT_KEY" \\
  -H "Content-Type: application/json" \\
  -d '{ "schema_version": 1, "install_id": "5f0c…", "app": { "version": "2.5.1" } }'

# → { "liveops_events": [ { "key": "${props.event.key}", "kind": "${props.event.kind}",
#       "priority": ${props.event.priority}, "starts_at_ms": …, "ends_at_ms": …,
#       "next_starts_at_ms": …, "payload": { … } } ], "server_time_ms": … }`)

const code = computed(() => (engine.value === 'unity' ? unity.value : http.value))
</script>

<template>
  <div class="flex flex-col gap-3">
    <SegmentedControl
      v-model="engine"
      :options="ENGINES"
      class="self-start"
      label="Engine"
    />
    <CodeBlock :code="code" :lang="engine === 'unity' ? 'csharp' : 'shellscript'" :title="engine === 'unity' ? 'Unity · C#' : 'Any engine · HTTP'" />
    <p class="m-0 text-[11px] leading-4 text-ink/55">
      <template v-if="engine === 'unity'">
        An event is only in <span class="font-mono">Active</span> for a player inside its audience and its rollout, during a window.
        Its windows are worked out on the server and compared against the server's clock, so a player who moves their
        device clock forward does not open it early. <span class="font-mono">Upcoming</span> lists what starts soon, for a teaser.
      </template>
      <template v-else>
        For an engine without an SDK. The response carries the events on for this player now, with the current window and the next,
        and none of the schedule or the audience rules.
      </template>
    </p>
  </div>
</template>
