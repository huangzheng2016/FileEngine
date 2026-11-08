// The one chart library. Only what is registered here ends up in the bundle;
// register more chart types or components here, never in a page.
import { init, use, type EChartsCoreOption } from 'echarts/core'
import { BarChart, GraphChart, LineChart, PieChart } from 'echarts/charts'
import { DataZoomComponent, GridComponent, LegendComponent, TooltipComponent } from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'

use([
  LineChart,
  BarChart,
  PieChart,
  GraphChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  CanvasRenderer,
])

export { init, type EChartsCoreOption }

// Colors and type come from tokens.css, so charts follow the brand and dark mode.
export function themeFromTokens(): object {
  const css = getComputedStyle(document.documentElement)
  const v = (name: string) => css.getPropertyValue(name).trim()
  const axis = {
    axisLine: { lineStyle: { color: v('--border-strong') } },
    axisTick: { lineStyle: { color: v('--border-strong') } },
    axisLabel: { color: v('--text-muted') },
    splitLine: { lineStyle: { color: v('--border') } },
  }
  return {
    color: Array.from({ length: 8 }, (_, i) => v(`--chart-${i + 1}`)),
    backgroundColor: 'transparent',
    textStyle: { fontFamily: v('--font-sans'), color: v('--text-secondary') },
    categoryAxis: axis,
    valueAxis: axis,
    timeAxis: axis,
    logAxis: axis,
    legend: { textStyle: { color: v('--text-secondary') } },
    tooltip: {
      backgroundColor: v('--surface-raised'),
      borderColor: v('--border'),
      textStyle: { color: v('--text') },
    },
  }
}
