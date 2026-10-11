import { screen, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { translate } from '@/i18n'
import { browser } from '@/lib/browser'
import { renderApp } from './render'

describe('sign-in and sign-out', () => {
  it('shows the sign-in screen on 401 and returns to the same page', async () => {
    const assign = vi.spyOn(browser, 'assign').mockImplementation(() => {})
    const { user } = renderApp('/me/sessions?from=mail', { setup: (db) => (db.signedIn = false) })

    const button = await screen.findByRole('button', { name: 'Sign in with Kite Plus' })
    expect(screen.getByRole('heading', { name: 'Sign in to Kite Plus Comments' })).toBeInTheDocument()
    await user.click(button)
    expect(assign).toHaveBeenCalledWith('/auth/login?return=%2Fme%2Fsessions%3Ffrom%3Dmail')
  })

  it('signs out with the CSRF token and says so', async () => {
    const { user, writes } = renderApp('/me')
    await user.click(await screen.findByRole('button', { name: /Amigo/ }))
    await user.click(await screen.findByRole('menuitem', { name: 'Sign out' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Sign out' }))

    expect(await screen.findByText('You have signed out.')).toBeInTheDocument()
    const logout = writes().find((c) => c.path === '/auth/logout')
    expect(logout?.method).toBe('POST')
    expect(logout?.headers.get('X-CSRF-Token')).toBe('mock-csrf-token')
  })

  it('sends a reader whose session ended elsewhere back to sign-in', async () => {
    const { user, db } = renderApp('/me')
    await screen.findByRole('heading', { name: 'My comments' })
    db.signedIn = false
    await user.click(screen.getByRole('link', { name: 'Notifications' }))
    expect(await screen.findByText('Your session has ended. Please sign in again.')).toBeInTheDocument()
  })

  it('tells a signed-out visitor that a claim email is not for them', async () => {
    renderApp('/me/claims/41?t=demo-token', { setup: (db) => (db.signedIn = false) })
    expect(await screen.findByText('This email was not meant for you. You can ignore it.')).toBeInTheDocument()
    expect(screen.getByRole('button', { name: 'Sign in with Kite Plus' })).toBeInTheDocument()
  })
})

describe('language', () => {
  it('switches between English and Chinese', async () => {
    const { user } = renderApp('/me')
    await screen.findByRole('heading', { name: 'My comments' })
    await user.click(screen.getAllByRole('button', { name: 'Language' })[0])
    await user.click(await screen.findByRole('menuitemradio', { name: translate('zh-CN', 'lang.name') }))
    expect(
      await screen.findByRole('heading', { name: translate('zh-CN', 'myComments.title') })
    ).toBeInTheDocument()
    expect(document.documentElement.lang).toBe('zh-CN')
    expect(localStorage.getItem('kc-lang')).toBe('zh-CN')
  })
})
