/// <reference types="vite/client" />

// Wails runtime type declaration
interface Window {
  runtime: {
    Call: (method: string, ...args: any[]) => Promise<any>
    EventsOn: (event: string, callback: (...args: any[]) => void) => void
    EventsOff: (event: string) => void
  }
}
