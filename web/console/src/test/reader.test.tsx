import { screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { renderApp } from './render'

describe('reader center', () => {
  it('lists my comments with links back to #kc- anchors and their status', async () => {
    renderApp('/me')
    const list = await screen.findByRole('list', { name: 'My comments' })
    const items = within(list).getAllByRole('listitem')
    expect(items).toHaveLength(4)

    const pending = items.find((li) => li.textContent?.includes('zig build'))!
    expect(within(pending).getByText('Pending')).toBeInTheDocument()
    expect(within(pending).getByText('Shown on the page once it is approved.')).toBeInTheDocument()
    const view = within(pending).getByRole('link', { name: /View on the page/ })
    expect(view).toHaveAttribute('href', expect.stringMatching(/^https:\/\/lin\.dev\/posts\/zig-build#kc-\d+$/))
    expect(view).toHaveAttribute('target', '_blank')
  })

  it('deletes one of my comments after confirming', async () => {
    const { user, writes } = renderApp('/me')
    const list = await screen.findByRole('list', { name: 'My comments' })
    const pending = within(list).getAllByRole('listitem').find((li) => li.textContent?.includes('zig build'))!
    const button = within(pending).getByRole('button', { name: /^Delete this comment on / })
    await user.click(button)
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Delete this comment?')
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))
    await waitFor(() => expect(within(pending).getByText('This comment was deleted.')).toBeInTheDocument())
    expect(writes().at(-1)).toMatchObject({ method: 'DELETE', path: expect.stringMatching(/^\/api\/v1\/me\/comments\/\d+$/) })
    expect(within(pending).queryByRole('button', { name: /^Delete this comment on / })).toBeNull()
  })

  it('shows an empty state when there is nothing yet', async () => {
    renderApp('/me', { setup: (db) => db.comments.forEach((c) => (c.mine = false)) })
    expect(await screen.findByText('You have not posted any comments yet.')).toBeInTheDocument()
  })

  it('shows an error with retry when the list fails', async () => {
    const { user, db } = renderApp('/me', {
      setup: (db) => {
        // Comments whose thread is missing make the fake answer 500.
        db.threads = db.threads.filter((t) => t.id !== '952')
      },
    })
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load')
    db.comments = db.comments.filter((c) => c.thread_id !== '952')
    await user.click(screen.getByRole('button', { name: 'Retry' }))
    expect(await screen.findByRole('list', { name: 'My comments' })).toBeInTheDocument()
  })

  it('marks all notifications as read', async () => {
    const { user, writes } = renderApp('/me/notifications')
    const list = await screen.findByRole('list', { name: 'Notifications' })
    expect(within(list).getAllByText('Unread')).toHaveLength(2)
    await user.click(screen.getByRole('button', { name: 'Mark all as read' }))
    await waitFor(() => expect(within(list).queryAllByText('Unread')).toHaveLength(0))
    expect(writes()).toContainEqual(
      expect.objectContaining({ path: '/api/v1/me/notifications/read', body: { all: true } })
    )
  })

  it('mutes a thread from a notification', async () => {
    const { user, writes } = renderApp('/me/notifications')
    const list = await screen.findByRole('list', { name: 'Notifications' })
    const first = within(list).getAllByRole('listitem')[0]
    await user.click(within(first).getByRole('button', { name: 'Actions' }))
    await user.click(await screen.findByRole('menuitem', { name: 'Mute this thread' }))
    await waitFor(() => expect(within(first).getByText('Muted')).toBeInTheDocument())
    expect(writes().at(-1)).toMatchObject({ method: 'PUT', path: '/api/v1/me/mutes/901' })
  })

  it('signs out of one site, then of all sites', async () => {
    const { user, writes } = renderApp('/me/sessions')
    const list = await screen.findByRole('list', { name: 'Signed-in sites' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(3)

    await user.click(screen.getByRole('button', { name: "Sign out of Lin's Tech Notes" }))
    let dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent(/Safari . iOS/)
    await user.click(within(dialog).getByRole('button', { name: 'Sign out' }))
    await waitFor(() => expect(within(list).getAllByRole('listitem')).toHaveLength(2))
    expect(writes().at(-1)).toMatchObject({
      method: 'DELETE',
      path: '/api/v1/me/site-sessions/b1a2c3d4-0002-4000-8000-000000000002',
    })

    await user.click(screen.getByRole('button', { name: 'Sign out of all sites' }))
    dialog = await screen.findByRole('alertdialog')
    await user.click(within(dialog).getByRole('button', { name: 'Sign out of all sites' }))
    expect(await screen.findByText('You are not signed in on any blog.')).toBeInTheDocument()
    expect(writes().at(-1)).toMatchObject({ method: 'DELETE', path: '/api/v1/me/site-sessions' })
  })

  it('saves only the profile fields that changed', async () => {
    const { user, writes } = renderApp('/me/profile')
    const name = await screen.findByRole('textbox', { name: 'Display name' })
    await user.clear(name)
    await user.type(name, 'Amigo Lee')
    await user.click(screen.getByRole('switch', { name: 'Email me when someone replies' }))
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Profile saved')).toBeInTheDocument()
    expect(writes().at(-1)).toMatchObject({
      method: 'PATCH',
      path: '/api/v1/me',
      body: { display_name: 'Amigo Lee', notify_replies: false },
    })
  })

  it('rejects an empty display name before sending', async () => {
    const { user, writes } = renderApp('/me/profile')
    const name = await screen.findByRole('textbox', { name: 'Display name' })
    await user.clear(name)
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Required')).toBeInTheDocument()
    expect(name).toHaveAttribute('aria-invalid', 'true')
    expect(writes()).toHaveLength(0)
  })
})

describe('deleting my data', () => {
  it('blocks deletion while I am the only owner of a site', async () => {
    const { db } = renderApp('/me/data')
    expect(await screen.findByText('Deal with your sites first')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: db.sites[0].name })).toHaveAttribute(
      'href',
      '/console/sites/7f3c2a1e-5b8d-4c6f-9a2e-1d4b6c8e0f12/data'
    )
    expect(screen.getByRole('button', { name: 'Delete my data...' })).toBeDisabled()
  })

  it('deletes with the chosen comment handling after typing the word', async () => {
    const { user, writes } = renderApp('/me/data', {
      setup: (db) => db.sites.forEach((s) => s.mine && (s.status = 'deleted')),
    })
    await user.click(await screen.findByRole('radio', { name: 'Keep them, anonymized' }))
    // The first group is the standing setting; the second is this deletion.
    const radios = screen.getAllByRole('radio', { name: 'Keep them, anonymized' })
    await user.click(radios[1])
    await user.click(screen.getByRole('button', { name: 'Delete my data...' }))
    const dialog = await screen.findByRole('alertdialog')
    expect(dialog).toHaveTextContent('Comments: Keep them, anonymized')
    const confirm = within(dialog).getByRole('button', { name: 'Delete permanently' })
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox'), 'delete')
    await user.click(confirm)

    expect(await screen.findByRole('heading', { name: 'Your data has been deleted' })).toBeInTheDocument()
    expect(writes()).toContainEqual(
      expect.objectContaining({ method: 'PATCH', path: '/api/v1/me', body: { on_account_delete: 'anonymize' } })
    )
    expect(writes().at(-1)).toMatchObject({
      method: 'DELETE',
      path: '/api/v1/me',
      body: { comments: 'anonymize' },
    })
    expect(screen.getByRole('link', { name: 'Open id.kite.plus' })).toHaveAttribute('href', 'https://id.kite.plus/')
  })

  it('shows the block when the server answers sole_owner', async () => {
    const { user } = renderApp('/me/data', {
      setup: (db) => db.sites.forEach((s) => s.mine && (s.role = 'moderator')),
    })
    await user.click(await screen.findByRole('button', { name: 'Delete my data...' }))
    const dialog = await screen.findByRole('alertdialog')
    await user.type(within(dialog).getByRole('textbox'), 'delete')
    await user.click(within(dialog).getByRole('button', { name: 'Delete permanently' }))
    expect(await screen.findByText('Deal with your sites first')).toBeInTheDocument()
  })
})

describe('claims', () => {
  it('verifies and confirms a claim from the email link', async () => {
    const { user, writes } = renderApp('/me/claims/41?t=demo-token')
    await user.click(await screen.findByRole('button', { name: 'Verify' }))
    expect(await screen.findByText('Found 3 guest comments left with this address:')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Claim these comments' }))
    expect(await screen.findByText(/Claimed 3 comments/)).toBeInTheDocument()
    expect(writes().map((c) => c.path)).toEqual(['/api/v1/me/claims/41/verify', '/api/v1/me/claims/41/confirm'])
    expect(writes()[0].body).toEqual({ token: 'demo-token' })
  })

  it('claims comments left with the account email without a mail round trip', async () => {
    const { user, writes } = renderApp('/me/claims')
    expect(await screen.findByText(/Found 2 guest comments left with amigo@example.com/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Claim these comments' }))
    expect(await screen.findByText(/Claimed 2 comments/)).toBeInTheDocument()
    expect(writes()[0]).toMatchObject({ path: '/api/v1/me/claims', body: { email: 'amigo@example.com' } })
    expect(writes()[1].path).toMatch(/^\/api\/v1\/me\/claims\/\d+\/confirm$/)
  })

  it('says the email is not for this account when the token does not match', async () => {
    const { user } = renderApp('/me/claims/41?t=wrong')
    await user.click(await screen.findByRole('button', { name: 'Verify' }))
    expect(await screen.findByText('This email was not meant for you. You can ignore it.')).toBeInTheDocument()
  })
})
