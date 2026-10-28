import { get, post } from '@/utils/request'

export const Login = (params) => post('/api/v1/user/login', params)
export const Register = (params) => post('/api/v1/user/register', params)
export const GetUserProfileApi = (params) => get('/api/v1/profile', params)
