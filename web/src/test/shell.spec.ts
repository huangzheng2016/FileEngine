import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises } from '@vue/test-utils'

const auth = vi.hoisted(() => ({ me: vi.fn(), logout: vi.fn() }))
const nav = vi.hoisted(() => ({ replace: vi.fn(), resetSignIn: vi.fn() }))

vi.mock('@/api/auth', () => auth)
vi.mock('@/router', () => ({ router: { replace: nav.replace }, resetSignIn: nav.resetSignIn }))

beforeEach(() => {
  vi.resetModules()
  vi.clearAllMocks()
})

describe('shell user', () => {
  it('offers sign-out on the server and clears the session before the login page', async () => {
    auth.me.mockResolvedValue({ authenticated: true, desktop: false })
    auth.logout.mockResolvedValue({})
    const { useShellUser } = await import('@/base/app')
    const user = useShellUser()
    await flushPromises()
    expect(user.name.value).not.toBe('')
    await user.logout()
    expect(auth.logout).toHaveBeenCalled()
    expect(nav.resetSignIn).toHaveBeenCalled()
    expect(nav.replace).toHaveBeenCalledWith('/login')
  })

  it('stays signed in when sign-out fails', async () => {
    auth.me.mockResolvedValue({ authenticated: true, desktop: false })
    auth.logout.mockRejectedValue(new Error('offline'))
    const { useShellUser } = await import('@/base/app')
    await useShellUser().logout()
    expect(nav.resetSignIn).not.toHaveBeenCalled()
    expect(nav.replace).not.toHaveBeenCalled()
  })

  it('hides sign-out in the desktop window', async () => {
    auth.me.mockResolvedValue({ authenticated: true, desktop: true })
    const { useShellUser } = await import('@/base/app')
    const user = useShellUser()
    await flushPromises()
    expect(user.name.value).toBe('')
  })
})
