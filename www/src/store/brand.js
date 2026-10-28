// store/brand.js
import { getBrandApi } from '@/api/brand'
import { defineStore } from 'pinia'

/**
 * 品牌配置（自定义 Logo）。
 *
 * 设计要点：
 * 1. 两个字段为空串 = 未配置，页面回落内置默认图——**不用 loading 态挡渲染**。
 *    Logo 是纯装饰元素，为了等一个接口把首屏（尤其是登录页）卡住不值得。
 * 2. 接口失败静默：拿不到就用默认图，最多是没换 Logo，不能让页面报错。
 * 3. loaded 在发起请求**之前**置位，避免登录页 + 布局同时挂载时并发打两次接口。
 */
export const useBrandStore = defineStore('brand', {
  state: () => ({
    loginLogoUrl: '',
    sidebarLogoUrl: '',
    brandName: '',
    homeTitle: '',
    loaded: false,
  }),
  actions: {
    async fetchBrand(force = false) {
      if (this.loaded && !force) return
      this.loaded = true
      try {
        const res = await getBrandApi()
        this.applyBrand(res?.data)
      } catch {
        // 静默失败，保留空串 → 页面用内置默认图 / 默认文案
        this.loginLogoUrl = ''
        this.sidebarLogoUrl = ''
        this.brandName = ''
        this.homeTitle = ''
      }
    },

    // 上传 / 保存后由接口返回值直接覆盖，省一次回读
    applyBrand(data) {
      const payload = data || {}
      this.loginLogoUrl = payload.login_logo_url || ''
      this.sidebarLogoUrl = payload.sidebar_logo_url || ''
      this.brandName = payload.brand_name || ''
      this.homeTitle = payload.home_title || ''
      this.loaded = true
    },
  },
})
