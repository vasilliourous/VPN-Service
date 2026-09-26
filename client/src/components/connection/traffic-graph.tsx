import { Paper, alpha, useTheme } from '@mui/material'
import { useMemo, useRef } from 'react'

import { useVerge } from '@/hooks/use-verge'

import {
  EnhancedCanvasTrafficGraph,
  type EnhancedCanvasTrafficGraphRef,
} from './enhanced-canvas-traffic-graph'

/**
 * The live traffic graph.
 *
 * Split out of the old combined dashboard so the Connection screen owns the
 * graph and the Account screen owns the totals. The canvas renderer itself is
 * unchanged — it is 1,190 lines of drawing code with no knowledge of which
 * screen mounts it, and rewriting it was never the point of this rework.
 *
 * Clicking toggles the graph's own style (line ⇄ filled); that behaviour lives
 * inside the canvas component and is preserved.
 */
export const TrafficGraph = () => {
  const theme = useTheme()
  const trafficRef = useRef<EnhancedCanvasTrafficGraphRef>(null)
  const { verge } = useVerge()

  // Respect the user's stored preference. Locus keeps this setting, but it now
  // defaults on: the graph is the clearest evidence the tunnel is carrying
  // traffic, which is the question a student actually has.
  const showGraph = verge?.traffic_graph ?? true

  return useMemo(() => {
    if (!showGraph) return null

    return (
      <Paper
        elevation={0}
        sx={{
          height: 150,
          cursor: 'pointer',
          border: `1px solid ${alpha(theme.palette.divider, 0.2)}`,
          borderRadius: 2,
          overflow: 'hidden',
        }}
        onClick={() => trafficRef.current?.toggleStyle()}
      >
        <div style={{ height: '100%', position: 'relative' }}>
          <EnhancedCanvasTrafficGraph ref={trafficRef} />
        </div>
      </Paper>
    )
  }, [showGraph, theme.palette.divider])
}
