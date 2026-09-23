// The staff console's screens, in the order the drawer shows them: folding groups, each
// under one glyph, the way idenzy's drawer nests its screens. Settings is one drawer item:
// Staff access is its only tab today, and the group still names it for the breadcrumb and
// the tab title.
export const NAV = defineNavigation({
  sections: [{
    entries: [
      { title: 'Overview', to: '/', icon: 'material-symbols:space-dashboard-outline' },
      {
        id: 'customers',
        title: 'Customers',
        icon: 'material-symbols:domain',
        children: [
          { title: 'Organizations', to: '/organizations' },
          { title: 'Projects', to: '/projects' },
          { title: 'Entitlements', to: '/entitlements' },
          { title: 'Billing', to: '/billing' }
        ]
      },
      {
        id: 'pipeline',
        title: 'Pipeline',
        icon: 'material-symbols:monitoring',
        children: [
          { title: 'Ingestion volume', to: '/ingestion' },
          { title: 'Queue backlog', to: '/queue' },
          { title: 'ClickHouse health', to: '/clickhouse' },
          { title: 'Storage', to: '/storage' },
          { title: 'Retention jobs', to: '/retention-jobs' }
        ]
      },
      {
        id: 'integrity',
        title: 'Integrity',
        icon: 'material-symbols:verified-user-outline',
        children: [
          { title: 'Webhook failures', to: '/webhooks' },
          { title: 'Purchase verification', to: '/purchases' },
          { title: 'Abuse / rate limits', to: '/abuse' }
        ]
      },
      {
        id: 'support',
        title: 'Support',
        icon: 'material-symbols:support-agent',
        children: [
          { title: 'Support tooling', to: '/support' },
          { title: 'Audit access', to: '/audit' }
        ]
      }
    ]
  }],
  footer: [{
    id: 'settings',
    title: 'Settings',
    icon: 'material-symbols:settings-outline',
    to: '/settings/staff-access',
    children: [
      { title: 'Staff access', to: '/settings/staff-access' }
    ]
  }]
})
