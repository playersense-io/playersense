<template>
  <div>
    <UPageHero
      title="One SDK. One control plane for your game."
      description="Analytics, feature flags, LiveOps and player insight — without building the pipeline yourself. Ship one lightweight SDK and get a connected set of services behind it."
      :links="[{
        label: 'Get started',
        to: 'https://app.playersense.io/signup',
        trailingIcon: 'i-lucide-arrow-right',
        size: 'xl'
      }, {
        label: 'SDK install guide',
        to: '/docs/sdk',
        size: 'xl',
        color: 'neutral',
        variant: 'subtle'
      }]"
    />

    <UPageSection
      id="features"
      title="A shared operating layer for your studio"
      description="One integration, a connected set of services behind it."
      :features="[{
        icon: 'i-lucide-line-chart',
        title: 'Analytics',
        description: 'Sessions, DAU/WAU/MAU, retention, revenue and cohort analysis, without hand-rolling a pipeline on top of raw events.'
      }, {
        icon: 'i-lucide-flag',
        title: 'Feature flags & rollouts',
        description: 'Draft, validate and publish flags and audience segments to a running game — every rule is checked when it is written, not just when it ships.'
      }, {
        icon: 'i-lucide-calendar-clock',
        title: 'LiveOps',
        description: 'Schedule events and configuration changes ahead of time and let the server decide when they take effect.'
      }, {
        icon: 'i-lucide-shield-check',
        title: 'Built with privacy in mind',
        description: 'Consent and data classification are part of the schema, not a policy layered on afterward, so compliance obligations are addressed by the architecture itself.'
      }, {
        icon: 'i-lucide-gauge',
        title: 'Built for scale',
        description: 'The same contracts and data model are meant to carry a studio from its first players to a very large one, without a rewrite along the way.'
      }, {
        icon: 'i-lucide-blocks',
        title: 'One SDK, growing platform support',
        description: 'A single Rust core with thin, idiomatic bindings per engine — so behavior stays consistent instead of being reimplemented per platform.'
      }]"
    />

    <UPageSection>
      <UPageCTA
        title="Bring your game, we'll handle the pipeline"
        description="Install the SDK and connect your first project."
        variant="subtle"
        :links="[{
          label: 'Get started',
          to: 'https://app.playersense.io/signup',
          trailingIcon: 'i-lucide-arrow-right',
          color: 'neutral'
        }]"
      />
    </UPageSection>
  </div>
</template>
