import './assets/styles/index.scss'

import { ResizeObserver } from '@juggle/resize-observer'
import { Box, CircularProgress, createTheme, CssBaseline, ThemeProvider } from '@mui/material'
import { ComposeContextProvider } from 'foxact/compose-context-provider'
import React, { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { RouterProvider } from 'react-router'
import { SWRConfig } from 'swr'
import { MihomoWebSocket } from 'tauri-plugin-mihomo-api'

import { BaseErrorBoundary } from './components/base'
import { hideInitialOverlay } from './pages/_layout/utils/initial-loading-overlay'
import { router } from './pages/_routers'
import ActivationScreen from './pages/activation'
import { preloadHomePageCards } from './pages/home'
import { AppDataProvider } from './providers/app-data-provider'
import { WindowProvider } from './providers/window'
import { FALLBACK_LANGUAGE, initializeLanguage } from './services/i18n'
import { locusStatus } from './services/locus'
import {
  preloadAppData,
  resolveThemeMode,
  getPreloadConfig,
} from './services/preload'
import { swrConfig } from './services/query-client'
import {
  LoadingCacheProvider,
  ThemeModeProvider,
  UpdateStateProvider,
} from './services/states'
import { disableWebViewShortcuts } from './utils/disable-webview-shortcuts'

if (!window.ResizeObserver) {
  window.ResizeObserver = ResizeObserver
}

const mainElementId = 'root'
const container = document.getElementById(mainElementId)

if (!container) {
  throw new Error(`No container '${mainElementId}' found to render application`)
}

disableWebViewShortcuts()

const initializeApp = (initialThemeMode: 'light' | 'dark') => {
  const root = createRoot(container)

  // The gate wraps everything, so an unactivated device cannot reach the
  // router, the profile machinery or the connect controls by any route — not
  // just by the navigation being hidden. "Render nothing else until activated"
  // is a much easier property to hold than "every page remembers to check".
  const Shell = () => {
    const [activated, setActivated] = useState<boolean | null>(null)

    // Remove the initial loading overlay once this component has painted.
    //
    // This MUST live here, not in `Layout`. The overlay is an opaque full-screen
    // element at `z-index: 9999` (see `index.html`), and it is only removed by
    // `hideInitialOverlay()`. That used to be called from `useLoadingOverlay`
    // inside `Layout` — but `Layout` is reached through `RouterProvider`, which
    // renders only in the `activated === true` branch below. So on an
    // unactivated device (every fresh install, and the state a student sees
    // first) the overlay was never removed and covered the activation screen
    // permanently: a blank window over a working app. The DOM was correct, the
    // assets all loaded 200, and nothing threw — which is why this presented as
    // an unexplained empty window rather than as an error.
    //
    // The effect is keyed on nothing because it needs to run exactly once, on
    // first mount, whichever branch then renders.
    useEffect(() => {
      hideInitialOverlay()
    }, [])

    useEffect(() => {
      locusStatus()
        .then((status) => setActivated(status.activated))
        // If we cannot read the state, assume NOT activated. The alternative
        // shows a VPN UI that cannot work, which is worse than asking for a code
        // the student already has on their card.
        .catch(() => setActivated(false))
    }, [])

    if (activated === null) {
      return (
        <Box
          sx={{
            width: '100vw',
            height: '100vh',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
          }}
        >
          <CircularProgress size={28} />
        </Box>
      )
    }

    if (!activated) {
      return <ActivationScreen onActivated={() => setActivated(true)} />
    }

    const contexts = [
      <ThemeModeProvider key="theme" initialState={initialThemeMode} />,
      <LoadingCacheProvider key="loading" />,
      <UpdateStateProvider key="update" />,
    ]

    return (
      <ComposeContextProvider contexts={contexts}>
        <BaseErrorBoundary>
          <SWRConfig value={swrConfig}>
            <WindowProvider>
              <AppDataProvider>
                <RouterProvider router={router} />
              </AppDataProvider>
            </WindowProvider>
          </SWRConfig>
        </BaseErrorBoundary>
      </ComposeContextProvider>
    )
  }

  root.render(
    <React.StrictMode>
      <ThemeProvider theme={createTheme()}>
        <CssBaseline />
        <BaseErrorBoundary>
          <Shell />
        </BaseErrorBoundary>
      </ThemeProvider>
    </React.StrictMode>,
  )
}

const bootstrap = async () => {
  const appDataPromise = preloadAppData()
  void preloadHomePageCards()

  const { initialThemeMode } = await appDataPromise
  initializeApp(initialThemeMode)
}

bootstrap().catch((error) => {
  console.error(
    '[main.tsx] App bootstrap failed, falling back to default language:',
    error,
  )
  initializeLanguage(FALLBACK_LANGUAGE)
    .catch((fallbackError) => {
      console.error(
        '[main.tsx] Fallback language initialization failed:',
        fallbackError,
      )
    })
    .finally(() => {
      initializeApp(resolveThemeMode(getPreloadConfig()))
    })
})

// Error handling
window.addEventListener('error', (event) => {
  console.error('[main.tsx] Global error:', event.error)
})

window.addEventListener('unhandledrejection', (event) => {
  console.error('[main.tsx] Unhandled promise rejection:', event.reason)
})

// Page close/refresh events
window.addEventListener('beforeunload', () => {
  // Clean up all WebSocket instances to prevent memory leaks
  MihomoWebSocket.cleanupAll()
})

// Page loaded event
window.addEventListener('DOMContentLoaded', () => {
  // Clean up all WebSocket instances to prevent memory leaks
  MihomoWebSocket.cleanupAll()
})
