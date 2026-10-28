import { get } from '@/utils/request'

export const getHomeDashboardApi = (params, config = {}) => get('/api/v1/home/dashboard', params, config)
