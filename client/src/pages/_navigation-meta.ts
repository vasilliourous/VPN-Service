/**
 * The two destinations Locus has.
 *
 * This used to be four (Home, Proxies, Logs, Settings), and three of them were
 * Clash Verge surfaces a student should never meet: a node picker for a product
 * with one server per tier, a raw core log, and a settings page exposing core
 * choice, TUN, system proxy, ports and DNS.
 *
 * Kept as a named map rather than inlined in `_navigation.tsx` because the
 * label keys are also referenced by the generated i18n key list. Renaming a key
 * here without updating the locale files is caught by `scripts/cleanup-unused-i18n.mjs`.
 */
export const navigationItems = {
  connection: {
    // Reuses the existing `layout.components.navigation.tabs.home` key so no
    // locale file needs a new tab entry; the label text is "Home", which is
    // still the right word for the first tab of a VPN app.
    label: 'layout.components.navigation.tabs.home',
    path: '/',
  },
  account: {
    label: 'layout.components.navigation.tabs.settings',
    path: '/account',
  },
} as const
