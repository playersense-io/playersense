// The customer dashboard's screens, in the order the drawer shows them: folding groups,
// each under one glyph, the way idenzy's drawer nests its screens. Settings is one drawer
// item and seven tabs: the rail inside the page moves between them, and the group still
// names each one for the breadcrumb and the tab title.
export const NAV = defineNavigation({
  sections: [{
    entries: [
      { title: 'Overview', to: '/', icon: 'material-symbols:space-dashboard-outline' },
      { title: 'Realtime', to: '/realtime', icon: 'material-symbols:bolt-outline' },
      // The game's own logs, exceptions and crashes: a place of its own rather than a corner of
      // Analytics, because it is read by the people who fix the game, not the ones who grow it.
      // Two screens, so an issue's page sits under Issues in the breadcrumb.
      {
        id: 'logs',
        title: 'Cloud Logging',
        icon: 'material-symbols:receipt-long-outline',
        children: [
          { title: 'Logs', to: '/logs' },
          { title: 'Issues', to: '/logs/issues' }
        ]
      },
      {
        id: 'analytics',
        title: 'Analytics',
        icon: 'material-symbols:analytics-outline',
        children: [
          { title: 'Events', to: '/analytics/events' },
          { title: 'Players', to: '/analytics/players' },
          { title: 'Sessions', to: '/analytics/sessions' }
        ]
      },
      {
        id: 'insights',
        title: 'Insights',
        icon: 'material-symbols:insights',
        children: [
          { title: 'Retention', to: '/insights/retention' },
          { title: 'Funnels', to: '/insights/funnels' },
          { title: 'Cohorts', to: '/insights/cohorts' },
          { title: 'Revenue', to: '/insights/revenue' }
        ]
      },
      {
        id: 'growth',
        title: 'Growth',
        icon: 'material-symbols:rocket-launch-outline',
        // Everything the game is told at runtime. Flags and Live Ops events are authored against the
        // same segments and shipped in the same document, which Publishing sends to an
        // environment; so the five sit together, in the order a change travels through them.
        children: [
          { title: 'Feature flags', to: '/growth/flags' },
          { title: 'Segments', to: '/growth/segments' },
          { title: 'Experiments', to: '/growth/experiments' },
          { title: 'Live Ops', to: '/growth/liveops' },
          { title: 'Publishing', to: '/growth/publishing' }
        ]
      },
      {
        id: 'commerce',
        title: 'Data & commerce',
        icon: 'material-symbols:package-2-outline',
        children: [
          { title: 'Purchases', to: '/purchases' },
          { title: 'Webhooks', to: '/webhooks' },
          { title: 'Report Builder', to: '/reports' }
        ]
      }
    ]
  }],
  footer: [{
    id: 'settings',
    title: 'Settings',
    icon: 'material-symbols:settings-outline',
    to: '/settings',
    children: [
      { title: 'General', to: '/settings' },
      { title: 'Data retention', to: '/settings/data-retention' },
      { title: 'Members', to: '/settings/members' },
      { title: 'API keys', to: '/settings/api-keys' },
      { title: 'Notification channels', to: '/settings/notifications' },
      { title: 'Billing', to: '/settings/billing' },
      { title: 'Audit log', to: '/settings/audit-log' }
    ]
  }]
})
