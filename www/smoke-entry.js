// 冒烟入口：vite 以客户端模式打成 lib，供 jsdom 挂载
import { createApp, h } from 'vue'
import Chart from './src/views/monitor/components/MetricTrendChart.vue'

export function mount(el, props) {
  const app = createApp({ render: () => h(Chart, props) })
  app.mount(el)
  return app
}
