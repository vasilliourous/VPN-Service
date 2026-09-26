import { createHashRouter, RouteObject } from 'react-router'

import Layout from './_layout'
import { navItems } from './_navigation'

// MUST be a HASH router. In production the frontend is served by Tauri over the
// `tauri://localhost` custom protocol, which has no server behind it: a browser
// router requests a real document at `/proxies`, nothing answers, and the app
// breaks on any navigation or reload that is not the root path. The hash router
// keeps the route in the fragment (`#/proxies`), so the document path never
// changes and the protocol can always resolve it.
//
// This is load-bearing, not stylistic — do not "simplify" it back to
// createBrowserRouter.
export const router = createHashRouter([
  {
    path: '/',
    Component: Layout,
    children: navItems.map(
      (item) =>
        ({
          path: item.path,
          Component: item.Component,
        }) as RouteObject,
    ),
  },
])
