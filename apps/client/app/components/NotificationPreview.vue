<script setup lang="ts">
// A notification as it will look where it lands: a Slack or Discord post from the app, an
// email with its subject and table, a Teams card, a Telegram message, a PagerDuty incident, or
// the JSON a webhook receives. Drawn in the panel's own colours rather than each product's,
// so it reads as a preview and not as a screenshot of somebody else's app.
const props = defineProps<{
  channel: NotificationChannel
  message: NotificationMessage
  /** When it is sent, for the time on the post. */
  at: number
}>()

const time = computed(() => new Date(props.at).toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit' }))

const table = computed(() => props.message.table ?? null)

const payload = computed(() => JSON.stringify({
  type: 'report.notification',
  report: props.message.link.split('report=')[1] ?? '',
  title: props.message.title,
  body: props.message.body,
  severity: props.message.severity,
  rows: table.value?.rows.map(row => Object.fromEntries(table.value!.columns.map(column => [column.key, row[column.key] ?? null]))) ?? [],
  link: props.message.link,
  sent_at: new Date(props.at).toISOString()
}, null, 2))

const SEVERITY_TONE: Record<Severity, string> = {
  critical: 'bg-error/12 text-error',
  error: 'bg-error/10 text-error',
  warning: 'bg-warning/12 text-warning',
  info: 'bg-primary/10 text-primary'
}
</script>

<template>
  <!-- A webhook: the body, as sent. -->
  <CodeBlock v-if="channel.type === 'webhook'" :code="payload" :title="`POST ${targetWords(channel)}`" />

  <!-- PagerDuty: the incident it opens. -->
  <div v-else-if="channel.type === 'pagerduty'" class="flex flex-col gap-3 rounded-(--ps-radius-large) border border-line bg-surface p-4">
    <div class="flex items-center gap-2">
      <span :class="SEVERITY_TONE[message.severity]" class="rounded-(--ps-radius-x-small) px-1.5 text-[10px] leading-5 font-medium tracking-wide uppercase">{{ SEVERITIES[message.severity].label }}</span>
      <span class="text-[11px] text-ink/50">Triggered · {{ channel.name }}</span>
    </div>
    <span class="text-sm font-medium text-ink">{{ message.title }}</span>
    <span class="text-xs leading-5 whitespace-pre-line text-ink/70">{{ message.body }}</span>
    <dl class="m-0 grid grid-cols-[6rem_minmax(0,1fr)] gap-x-3 gap-y-1 text-[11px]">
      <dt class="text-ink/45">
        Source
      </dt>
      <dd class="m-0 font-mono text-ink/75">
        playersense.report
      </dd>
      <dt class="text-ink/45">
        Dedup key
      </dt>
      <dd class="m-0 font-mono text-ink/75">
        {{ message.link.split('report=')[1] ?? 'report' }}
      </dd>
    </dl>
  </div>

  <!-- Email. -->
  <div v-else-if="channel.type === 'email'" class="flex flex-col overflow-hidden rounded-(--ps-radius-large) border border-line bg-surface">
    <dl class="m-0 grid grid-cols-[4rem_minmax(0,1fr)] gap-x-3 gap-y-1 border-b border-line bg-ink/2 px-4 py-3 text-[11px]">
      <dt class="text-ink/45">
        From
      </dt>
      <dd class="m-0 text-ink/75">
        PlayerSense &lt;reports@playersense.io&gt;
      </dd>
      <dt class="text-ink/45">
        To
      </dt>
      <dd class="m-0 truncate text-ink/75">
        {{ (channel.config.addresses ?? []).join(', ') }}
      </dd>
      <dt class="text-ink/45">
        Subject
      </dt>
      <dd class="m-0 font-medium text-ink">
        {{ message.title }}
      </dd>
    </dl>
    <div class="flex flex-col gap-3 p-4">
      <span class="text-xs leading-5 whitespace-pre-line text-ink/80">{{ message.body }}</span>
      <NotificationTable v-if="table" :table="table" />
      <span class="flex flex-wrap items-center gap-2">
        <span class="inline-flex h-7 items-center rounded-(--ps-radius-small) bg-primary px-3 text-[11px] font-medium text-white">Open the report</span>
        <span v-if="message.attachment" class="inline-flex h-7 items-center gap-1.5 rounded-(--ps-radius-small) border border-line px-2 text-[11px] text-ink/70">
          <Icon aria-hidden="true" class="text-sm" name="material-symbols:attach-file" />
          {{ message.attachment.name }} · {{ message.attachment.rows.toLocaleString('en') }} rows
        </span>
      </span>
    </div>
  </div>

  <!-- A chat: Slack, Discord, Teams, Telegram, as the app's post. -->
  <div v-else class="flex gap-3 rounded-(--ps-radius-large) border border-line bg-surface p-4">
    <span aria-hidden="true" class="flex size-9 flex-none items-center justify-center rounded-(--ps-radius) bg-primary text-sm font-semibold text-white">PS</span>
    <div class="flex min-w-0 flex-1 flex-col gap-1.5">
      <span class="flex items-baseline gap-1.5">
        <span class="text-[13px] font-semibold text-ink">PlayerSense</span>
        <span class="rounded-(--ps-radius-x-small) bg-ink/8 px-1 text-[9px] leading-4 font-semibold tracking-wide text-ink/55 uppercase">{{ channel.type === 'slack' ? 'App' : 'Bot' }}</span>
        <span class="text-[11px] text-ink/40">{{ time }}</span>
        <span v-if="channel.type === 'slack'" class="ml-auto text-[11px] text-ink/40">{{ channel.config.channel }}</span>
      </span>
      <div :class="channel.type === 'slack' || channel.type === 'telegram' ? '' : 'rounded-(--ps-radius) bg-ink/4 p-3'" class="flex flex-col gap-1.5">
        <span class="text-[13px] font-semibold text-ink">{{ message.title }}</span>
        <span class="text-xs leading-5 whitespace-pre-line text-ink/80">{{ message.body }}</span>
        <NotificationTable v-if="table" :table="table" compact />
      </div>
      <span class="flex flex-wrap items-center gap-2 pt-0.5">
        <span class="inline-flex h-7 items-center rounded-(--ps-radius-small) border border-line px-2.5 text-[11px] font-medium text-ink/80">Open the report</span>
        <span v-if="message.attachment && channel.type !== 'teams'" class="inline-flex h-7 items-center gap-1.5 rounded-(--ps-radius-small) border border-line px-2 text-[11px] text-ink/70">
          <Icon aria-hidden="true" class="text-sm" name="material-symbols:attach-file" />
          {{ message.attachment.name }}
        </span>
      </span>
    </div>
  </div>
</template>
