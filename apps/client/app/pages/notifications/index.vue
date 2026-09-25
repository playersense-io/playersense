<script setup lang="ts">
// Notification rules: who hears about what. A rule routes events to channels: "when a config
// is published to production, tell #live-ops", "when a top-up fails, tell #finance and page
// the one on call". The project's rules and the organization's sit on one page, because a
// studio looks for "who hears about what" in one place.
//
// Above them, what is always on and no rule controls: the owners' email about the account.
// Saying it here is what stops somebody writing a rule for an email they already get, or
// wondering whether a suspension would reach anybody at all.
const { kinds } = useNotificationKinds()
const always = computed(() => kinds.value.filter(kind => kind.owners))
</script>

<template>
  <div class="flex flex-col gap-6">
    <PageHeader title="Notification rules">
      <template #description>
        Route what happens to the channels your team already reads: a publish to #live-ops, a failed top-up to #finance and PagerDuty. A rule points at channels, so a channel changed once is changed for every rule.
      </template>
      <template #actions>
        <v-btn
          class="text-xs"
          color="neutral"
          prepend-icon="material-symbols:add-link"
          size="small"
          to="/notifications/channels"
          variant="outlined"
        >
          Channels
        </v-btn>
      </template>
    </PageHeader>

    <section
      v-if="always.length"
      aria-label="Always on"
      class="flex flex-col gap-3 rounded-(--ps-radius-large) border border-line bg-surface px-5 py-4"
    >
      <div class="flex items-start gap-3">
        <Icon :aria-hidden="true" class="mt-0.5 flex-none text-lg text-primary" name="material-symbols:mark-email-unread-outline" />
        <div class="flex min-w-0 flex-col gap-1">
          <h2 class="m-0 text-sm font-medium text-ink">
            Always on: the account's owners are emailed
          </h2>
          <p class="m-0 text-xs leading-5 text-ink/60">
            Owners, admins and billing members get an email about each of these, at most once a day for each kind, whatever the rules below say. A rule of the organization adds Slack, PagerDuty or a team list on top.
          </p>
        </div>
      </div>
      <ul class="m-0 grid list-none gap-2 p-0 sm:grid-cols-2">
        <li v-for="kind in always" :key="kind.type" class="flex items-start gap-2.5 rounded-(--ps-radius) bg-ink/3 px-3 py-2">
          <span
            :class="{ critical: 'bg-error', error: 'bg-error', warning: 'bg-warning', info: 'bg-success' }[kind.severity]"
            aria-hidden="true"
            class="mt-1.5 size-1.5 flex-none rounded-full"
          />
          <span class="flex min-w-0 flex-col">
            <span class="text-xs font-medium text-ink">{{ kind.title }}</span>
            <span class="text-[11px] leading-4 text-ink/55">{{ kind.summary }}</span>
          </span>
        </li>
      </ul>
    </section>

    <NotificationRuleList scope="project" />
    <NotificationRuleList scope="organization" />
  </div>
</template>
