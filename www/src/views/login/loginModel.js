export const DEFAULT_AFTER_LOGIN_ROUTE = '/home'

export const resolveLoginTarget = (redirect) => {
  if (typeof redirect !== 'string' || !redirect.trim()) {
    return DEFAULT_AFTER_LOGIN_ROUTE
  }

  let decodedPath = redirect
  try {
    decodedPath = decodeURIComponent(redirect)
  } catch {
    decodedPath = redirect
  }

  if (!decodedPath.startsWith('/') || decodedPath.startsWith('/login')) {
    return DEFAULT_AFTER_LOGIN_ROUTE
  }

  return decodedPath
}
