// MyVPN Application — main entry point

import { createApp } from 'vue'
import './assets/styles.css'
import App from './App.vue'

const app = createApp(App)

// Global Vue error handler — renderer exceptions must never silently die in an
// embedded webview where there is no console for the user.
app.config.errorHandler = (err: unknown, _instance, info: string) => {
  console.error('[MyVPN] Vue error:', err, info)
}

// Uncaught promise rejections (e.g. a stray await outside a try/catch) are
// logged so support can see them via myvpn.log / diagnostics.
window.addEventListener('unhandledrejection', (event: PromiseRejectionEvent) => {
  console.error('[MyVPN] Unhandled rejection:', event.reason)
})

app.mount('#app')
