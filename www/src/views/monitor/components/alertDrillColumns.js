/**
 * 告警下钻弹窗（AlertDrillModal）表格列配置。
 * 告警中心计数卡/级别图例 与 监控大屏「新增告警」共用同一套列定义，
 * 保证列名、顺序、宽度一致；标题由各父页面通过 title / titlePrefix 独立控制。
 *
 * 列宽按「不换行」倒推（弹窗 body 可用 ~972，固定列合计已超过该值 → 表格横向滚动）：
 *   alert_id 172（含复制按钮）/ instance 324（两行：实例名 20 字符 + ip:port 20 字符；
 *     按 14px 字号实测：20 个全角字符 278px + 省略号 12px + 单元格左右 padding 32px）
 *   / severity 76 / triggered_at 182（单行 19 字符）/ status 92 / actions 66 / acked_by 88（短文本）。
 * 告警名称表头经 customHeaderCell 挂 .col-alert-name（white-space: nowrap）保证不折叠。
 */
export const ALERT_DRILL_COLUMNS = [
  { title: '告警ID', dataIndex: 'alert_id', key: 'alert_id', width: 172 },
  { title: '实例名称', key: 'instance', width: 324 },
  { title: '告警级别', key: 'severity', width: 76 },
  { title: '告警名称', key: 'name', customHeaderCell: () => ({ class: 'col-alert-name' }) },
  { title: '触发时间', dataIndex: 'triggered_at', key: 'triggered_at', width: 182 },
  { title: '状态', key: 'status', width: 92 },
  { title: '操作', key: 'actions', width: 66 },
  { title: '处理人', dataIndex: 'acked_by', key: 'acked_by', width: 88 },
]
