/**
 * 检查路由权限
 * 规则（重要：和后端 getEffectivePermissionCodesByUID 对齐）：
 *   1) 如果路由 meta.permission 命中：当前用户 permissions 含有其中一项 = 通过
 *   2) 如果父级路由自身 permission 没命中，但递归子路由有任意一项命中：父级仍然算通过
 *      （否则只授实例权限 system.instance.view，没授 system.access 时，整个系统管理菜单会被
 *       filterAsyncRoutes 直接剪掉，用户会以为授了却看不到 —— 后端已兜底自动补 system.access，
 *       但前端保留这个二次兜底更稳健）
 * @param {Array} permissions 用户权限列表
 * @param {Object} route 路由对象
 * @returns {Boolean}
 */
export function hasPermission(permissions, route) {
  const required = route.meta?.permission
  const permArr = Array.isArray(permissions) ? permissions : []
  if (required) {
    const requiredPermissions = Array.isArray(required) ? required : [required]
    if (requiredPermissions.some(p => permArr.includes(p))) {
      return true
    }
  }
  // 子路由递归：任何一个孩子通过，父级就当作通过（只用于"是否保留父菜单"判断）
  const children = Array.isArray(route.children) ? route.children : []
  for (const child of children) {
    if (hasPermission(permArr, child)) {
      return true
    }
  }
  // 没有任何 permission 要求的路由 = 允许通过（公共页面/入口）
  return !required
}

/**
 * 检查是否为超级管理员
 * @param {Object} user 用户信息
 * @returns {Boolean}
 */
export function isSuperAdmin(user) {
  return user && user.is_superuser === true
}

/**
 * 检查操作权限
 * @param {String} action 操作名称
 * @param {Array} permissions 权限列表
 * @returns {Boolean}
 */
export function hasAction(action, permissions = []) {
  return permissions.includes(action)
}

/**
 * 根路由（Layout 挂载点）：path 为 '/' 的顶层壳，children 就是全部一级菜单。
 * 它是整个后台的容器，即使一个子菜单都没有也必须保留，否则全站白屏/直接 404。
 */
function isRootLayoutRoute(route) {
  return route?.path === '/' || route?.name === 'Root'
}

/**
 * 递归过滤异步路由
 * @param {Array} routes 路由配置数组
 * @param {Boolean} isSuperuser 是否为超级管理员
 * @returns {Array} 过滤后的路由数组
 */
export function filterAsyncRoutes(routes, isSuperuser, permissions = []) {
  return routes.reduce((acc, route) => {
    const tmp = { ...route }

    if (!isSuperuser && !hasPermission(permissions, tmp)) {
      return acc
    }

    // 递归过滤子路由
    if (tmp.children?.length) {
      tmp.children = filterAsyncRoutes(tmp.children, isSuperuser, permissions)

      // 子项被权限全部剪掉 ⇒ 父级整体丢弃，不留「空壳菜单」。
      // 旧逻辑只在「无 component 或 带 redirect」时保留父级，于是带 redirect 的父路由
      // （如 view.admin → /admin/users）即便一个子项都不可见也会留在侧边栏：普通成员
      // 只因持有某条 system.<sub> 权限被后端兜底补了 system.access，就会出现
      // 「系统管理能点开但里面什么都没有」。根路由 '/' 例外，见 isRootLayoutRoute。
      if (tmp.children.length === 0 && !isRootLayoutRoute(tmp)) {
        return acc
      }
    }

    acc.push(tmp)
    return acc
  }, [])
}
