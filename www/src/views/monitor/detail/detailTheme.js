/**
 * 监控明细页颜色 token 引用层
 * =============================================================================
 * 本模块**不含任何色值**，只提供 CSS 变量名。色值的单一数据源分两层：
 *
 *   1. 基础层：系统全局 Design Token（:root，见 assets/scss/tokens.scss
 *      与 src/design/tokens.js），主色调 = --gi-color-primary；
 *   2. 强调层：由主色调在「明度 / 透明度」两个维度派生，用 color-mix() 在 CSS 里
 *      现算（见 DetailView.vue 末尾的主题 token 块），因此主题切换时由 CSS 级联
 *      自动同步，无需 JS 介入。
 *
 * 为什么 JS 里也要有一份变量名：曲线与图例圆点的颜色是作为 inline style 下发给
 * SVG <path> / <span> 的，必须由 JS 给出 —— 但给出的仍然是变量名，不是色值。
 *
 * 这些变量同时声明在 .monitor-detail（页面根）和 .monitor-detail-modal-wrap
 * （指标放大弹窗的包裹层）两个作用域上：弹窗被 a-modal teleport 到 body，
 * 脱离页面根节点，只有这样弹窗里的曲线 / 图例才能取到同一套 token。
 */

/** 系统主色调（本页唯一色相来源）。 */
export const PRIMARY_COLOR = 'var(--gi-primary)'

/**
 * 多曲线区分用的配色：淡色系 + 色相拉开。
 *
 * 设计取舍（两条要求同时满足，本来是互相拉扯的）：
 *   ·「区分明显」→ 靠**色相间隔**（青绿 / 蓝 / 紫 / 琥珀 / 翠绿 / 玫红，相邻至少 35°），
 *     而不是靠同色相的明度差 —— 明度差在低饱和时肉眼几乎分不出，且超过 3 档后
 *     浅档位会淡到看不见；
 *   ·「要淡色」→ 统一压到**中高明度 + 中低饱和**（L 48%~62%、S 42%~68%），
 *     避免高饱和撞色把一张监控卡画成调色盘，也避免多条曲线互相压过对方。
 *
 * 第 1 档仍从系统主色调派生（color-mix 混白提亮到与其它档位相当的明度），
 * 保证「卡片主色调 = 系统主色」这条基调不丢；第 2~6 档为固定淡色，
 * 在浅色 / 深色主题下都可读，故不随主色漂移。
 */
export const CURVE_COLORS = [
  'var(--gi-curve-1)',
  'var(--gi-curve-2)',
  'var(--gi-curve-3)',
  'var(--gi-curve-4)',
  'var(--gi-curve-5)',
  'var(--gi-curve-6)',
]

/**
 * 卡片档位 → 曲线描边色。
 * alert / error 是主色调的更深档位（本卡有告警 / 采集失败），
 * muted 代表「本卡没有数据」，是中性语义而非色相，保持中性灰。
 */
export const TONE_COLORS = {
  default: 'var(--gi-primary)',
  alert: 'var(--gi-primary-tone-alert)',
  error: 'var(--gi-primary-tone-error)',
  muted: 'var(--gi-muted)',
}

/** 按序号取曲线色；超出档位数时循环复用（仍不引入新色相）。 */
export function curveColorAt(index) {
  const safeIndex = Number.isFinite(index) && index >= 0 ? Math.floor(index) : 0
  return CURVE_COLORS[safeIndex % CURVE_COLORS.length]
}

/** 按档位取曲线 / 图例圆点颜色，未知档位回落到主色。 */
export function toneColorAt(tone) {
  return TONE_COLORS[tone] || TONE_COLORS.default
}
