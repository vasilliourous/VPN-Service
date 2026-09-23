/// <reference types="vite/client" />

import type { AppBindings } from '@/types'

// The Go backend bindings injected at `window.go.main.App`.
declare global {
  interface Window {
    go: {
      main: {
        App: AppBindings
      }
    }
    runtime: {
      Call: (method: string, ...args: any[]) => Promise<any>
      EventsOn: (event: string, callback: (...args: any[]) => void) => void
      EventsOff: (event: string) => void
      ClipboardSetText: (text: string) => Promise<void>
      Quit: () => void
    }
  }
}

export {}

