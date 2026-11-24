import { ApiError, request } from './http'
import type * as idl from './idl.gen'

// The sign-in probe and the login handle a 401 themselves; any other call that gets one ends the session.
export async function login(token: string) {
  await request<idl.AuthResp>('POST', '/api/auth/login', { body: { token }, skipAuthRedirect: true })
}

export function me() {
  return request<idl.MeResp>('GET', '/api/auth/me', { skipAuthRedirect: true })
}

export async function logout() {
  await request<idl.AuthResp>('POST', '/api/auth/logout', { skipAuthRedirect: true })
}

// Only a 401 means signed out; any other failure surfaces on the page that loads data.
export async function authed(): Promise<boolean> {
  try {
    await me()
    return true
  } catch (e) {
    return !(e instanceof ApiError && e.status === 401)
  }
}
