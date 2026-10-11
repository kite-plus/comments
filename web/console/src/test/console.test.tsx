import { fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { SITE_IDS } from '@/mocks/db'
import { renderApp } from './render'

const NOTES = `/console/sites/${SITE_IDS.notes}`

describe('sites and registration', () => {
  it('lists my sites with their status, and deleted ones apart', async () => {
    renderApp('/console')
    const list = await screen.findByRole('list', { name: 'My sites' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(3)
    expect(within(list).getByText('Awaiting approval')).toBeInTheDocument()
    expect(within(list).getByText('Unverified')).toBeInTheDocument()
    expect(screen.getByText('Deleted, still restorable')).toBeInTheDocument()
  })

  it('registers a site, then verifies it with the token from creation', async () => {
    const { user, writes, router } = renderApp('/console/new')
    await user.type(await screen.findByRole('textbox', { name: 'Site name' }), 'Test Blog')
    const origin = screen.getByLabelText('Primary origin')
    await user.type(origin, 'http://test.example.com')
    await user.click(screen.getByRole('button', { name: 'Register and verify' }))
    expect(await screen.findByText('Only https:// addresses are accepted.')).toBeInTheDocument()
    expect(writes()).toHaveLength(0)

    await user.clear(origin)
    await user.type(origin, 'https://Test.Example.com/')
    await user.click(screen.getByRole('button', { name: 'Add alias' }))
    await user.type(screen.getByLabelText('Alias 1'), 'https://www.test.example.com')
    await user.click(screen.getByRole('button', { name: 'Register and verify' }))

    expect(await screen.findByRole('heading', { name: 'Verify your site' })).toBeInTheDocument()
    expect(writes()[0]).toMatchObject({
      method: 'POST',
      path: '/api/v1/sites',
      body: {
        name: 'Test Blog',
        origin: 'https://test.example.com',
        aliases: ['https://www.test.example.com'],
        guest_policy: 'off',
      },
    })
    expect(router.state.location.pathname).toMatch(/^\/console\/sites\/[\w-]+\/verify$/)
    // The DNS tab lists a record per unverified origin.
    expect(await screen.findByText('_kite-comments.test.example.com')).toBeInTheDocument()
    expect(screen.getByText('_kite-comments.www.test.example.com')).toBeInTheDocument()
    expect(screen.getAllByText(/^kite-comments-site=kc_/)).toHaveLength(2)

    await user.click(screen.getByRole('button', { name: 'Check now' }))
    expect(await screen.findByText('All origins are verified')).toBeInTheDocument()
    expect(screen.getByText(/https:\/\/test\.example\.com verified \(DNS TXT record\)/)).toBeInTheDocument()
    expect(screen.getAllByText(/verified sites are approved by hand/).length).toBeGreaterThan(0)
  })

  it('shows what the server found when an origin fails', async () => {
    const { user } = renderApp(`/console/sites/${SITE_IDS.fresh}/verify`, {
      setup: (db) => {
        const site = db.sites.find((s) => s.id === SITE_IDS.fresh)!
        site.origins[1].origin = 'https://fail.example.org'
      },
    })
    expect(await screen.findByText(/The token is shown only once/)).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Generate token' }))
    expect(await screen.findByText('New token generated, valid for 7 days')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Check now' }))
    expect(await screen.findByText('https://fail.example.org did not pass')).toBeInTheDocument()
    expect(screen.getByText(/No TXT record at _kite-comments\.fail\.example\.org/)).toBeInTheDocument()
  })

  it('lists every result when no origin passes (422)', async () => {
    const { user } = renderApp(`/console/sites/${SITE_IDS.fresh}/verify`, {
      setup: (db) => {
        const site = db.sites.find((s) => s.id === SITE_IDS.fresh)!
        site.origins = site.origins.map((o, i) => ({ ...o, origin: `https://fail${i}.example.org` }))
      },
    })
    await user.click(await screen.findByRole('button', { name: 'Check now' }))
    expect(await screen.findByText('Verification did not pass')).toBeInTheDocument()
    expect(screen.getByText('https://fail0.example.org did not pass')).toBeInTheDocument()
    expect(screen.getByText('https://fail1.example.org did not pass')).toBeInTheDocument()
  })
})

describe('moderation', () => {
  it('approves a pending comment and refreshes the count', async () => {
    const { user, writes } = renderApp(`${NOTES}/comments`)
    const list = await screen.findByRole('list', { name: 'Moderation' })
    const items = within(list).getAllByRole('article')
    expect(items).toHaveLength(3)
    await user.click(within(items[0]).getByRole('button', { name: 'Approve' }))
    expect(await screen.findByText('Approved 1 comment')).toBeInTheDocument()
    await waitFor(() => expect(within(list).getAllByRole('article')).toHaveLength(2))
    expect(writes().at(-1)).toMatchObject({
      path: `/api/v1/sites/${SITE_IDS.notes}/comments/moderation`,
      body: { ids: [expect.any(String)], action: 'approve' },
    })
  })

  it('marks selected comments as spam in bulk', async () => {
    const { user, writes } = renderApp(`${NOTES}/comments`)
    await screen.findByRole('list', { name: 'Moderation' })
    await user.click(screen.getByRole('checkbox', { name: 'Select all on this page' }))
    expect(screen.getByText('3 selected')).toBeInTheDocument()
    const toolbar = screen.getByRole('toolbar', { name: 'Bulk actions' })
    await user.click(within(toolbar).getByRole('button', { name: 'Mark as spam' }))
    expect(await screen.findByText('Nothing is waiting for moderation.')).toBeInTheDocument()
    expect((writes().at(-1)?.body as { ids: string[] }).ids).toHaveLength(3)
  })

  it('bans the author of a comment and hides it', async () => {
    const { user, writes } = renderApp(`${NOTES}/comments`)
    const list = await screen.findByRole('list', { name: 'Moderation' })
    const first = within(list).getAllByRole('article')[0]
    await user.click(within(first).getByRole('button', { name: 'Ban...' }))
    const dialog = await screen.findByRole('dialog')
    expect(within(dialog).getByRole('radio', { name: /Account:/ })).toBeChecked()
    await user.click(within(dialog).getByRole('button', { name: 'Ban' }))
    expect(await screen.findByText('Banned')).toBeInTheDocument()
    const [ban, hide] = writes().slice(-2)
    expect(ban).toMatchObject({
      method: 'POST',
      path: `/api/v1/sites/${SITE_IDS.notes}/bans`,
      body: { kind: 'user', comment_id: expect.any(String), days: 0 },
    })
    expect(hide.body).toMatchObject({ action: 'hide' })
  })

  it('hides a reported comment and resolves the report', async () => {
    const { user, writes } = renderApp(`${NOTES}/reports`)
    const list = await screen.findByRole('list', { name: 'Reports' })
    expect(within(list).getByText('Abuse or harassment')).toBeInTheDocument()
    await user.click(within(list).getAllByRole('button', { name: 'Hide comment and resolve' })[0])
    expect(await screen.findByText('Comment hidden and report resolved')).toBeInTheDocument()
    expect(writes()).toEqual([
      expect.objectContaining({
        method: 'PATCH',
        path: `/api/v1/sites/${SITE_IDS.notes}/reports/62`,
        body: { status: 'resolved', action: 'hide' },
      }),
    ])
  })

  it('locks a thread and moves another to a new key', async () => {
    const { user, writes } = renderApp(`${NOTES}/threads`)
    const list = await screen.findByRole('list', { name: 'Threads' })
    const row = within(list).getAllByRole('listitem').find((li) => li.textContent?.includes('p:/about'))!
    await user.click(within(row).getByRole('button', { name: 'Lock' }))
    await waitFor(() => expect(within(row).getByRole('button', { name: 'Unlock' })).toBeInTheDocument())
    expect(writes().at(-1)).toMatchObject({ method: 'PATCH', body: { locked: true } })

    await user.click(within(row).getByRole('button', { name: /More actions/ }))
    await user.click(await screen.findByRole('menuitem', { name: 'Move to a new key...' }))
    const dialog = await screen.findByRole('dialog')
    const input = within(dialog).getByRole('textbox', { name: 'New key' })
    await user.type(input, '/about-me')
    await user.click(within(dialog).getByRole('button', { name: 'Move' }))
    expect(within(dialog).getByText('Keys start with k:, p: or h:.')).toBeInTheDocument()
    await user.clear(input)
    await user.type(input, 'p:/about-me')
    await user.click(within(dialog).getByRole('button', { name: 'Move' }))
    expect(await screen.findByText('Moved 2 comments')).toBeInTheDocument()
    expect(writes().at(-1)).toMatchObject({ method: 'POST', body: { to_key: 'p:/about-me' } })
  })

  it('corrects a title set by the first commenter, refusing a future date', async () => {
    const { user, writes } = renderApp(`${NOTES}/threads`)
    const list = await screen.findByRole('list', { name: 'Threads' })
    const row = within(list).getAllByRole('listitem').find((li) => li.textContent?.includes('p:/about'))!
    await user.click(within(row).getByRole('button', { name: /More actions/ }))
    await user.click(await screen.findByRole('menuitem', { name: 'Edit' }))
    const dialog = await screen.findByRole('dialog')
    const before = writes().length
    fireEvent.change(within(dialog).getByLabelText('Post published at'), { target: { value: '2999-01-01T00:00' } })
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(within(dialog).getByText('The post cannot be published in the future.')).toBeInTheDocument()
    expect(writes()).toHaveLength(before)
    fireEvent.change(within(dialog).getByLabelText('Post published at'), { target: { value: '2020-01-01T00:00' } })
    const title = within(dialog).getByRole('textbox', { name: 'Title' })
    await user.clear(title)
    await user.type(title, 'About me')
    await user.click(within(dialog).getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Thread saved')).toBeInTheDocument()
    expect(writes().at(-1)).toMatchObject({ method: 'PATCH', body: { title: 'About me', published_at: expect.stringMatching(/^2020-01-01T/) } })
  })

  it('lists reported comments in their own tab', async () => {
    renderApp(`${NOTES}/comments?status=reported`)
    const list = await screen.findByRole('list', { name: 'Moderation' })
    const items = within(list).getAllByRole('article')
    expect(items).toHaveLength(1)
    expect(within(items[0]).getByText('2 reports')).toBeInTheDocument()
  })

  it('undoes a thread move from the audit log', async () => {
    const { user, writes } = renderApp(`${NOTES}/threads`)
    const list = await screen.findByRole('list', { name: 'Threads' })
    const row = within(list).getAllByRole('listitem').find((li) => li.textContent?.includes('p:/about'))!
    await user.click(within(row).getByRole('button', { name: /More actions/ }))
    await user.click(await screen.findByRole('menuitem', { name: 'Move to a new key...' }))
    const dialog = await screen.findByRole('dialog')
    await user.type(within(dialog).getByRole('textbox', { name: 'New key' }), 'p:/about-me')
    await user.click(within(dialog).getByRole('button', { name: 'Move' }))
    await screen.findByText('Moved 2 comments')

    await user.click(screen.getByRole('link', { name: 'Audit log' }))
    const log = await screen.findByRole('list', { name: 'Audit log' })
    expect(within(log).getAllByText('moved a thread')).toHaveLength(1)
    await user.click(within(log).getByRole('button', { name: 'Undo' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Undo' }))
    expect(await screen.findByText('Move undone')).toBeInTheDocument()
    expect(writes().at(-1)).toMatchObject({ method: 'POST', path: expect.stringMatching(/\/events\/\d+\/undo$/) })
  })

  it('lifts a site ban but not a platform-wide one', async () => {
    const { user, writes } = renderApp(`${NOTES}/bans`)
    const list = await screen.findByRole('list', { name: 'Bans' })
    expect(within(list).getAllByRole('listitem')).toHaveLength(3)
    expect(within(list).getAllByRole('button', { name: 'Lift' })).toHaveLength(2)
    await user.click(within(list).getAllByRole('button', { name: 'Lift' })[0])
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Lift' }))
    expect(await screen.findByText('Ban lifted')).toBeInTheDocument()
    expect(writes().at(-1)?.method).toBe('DELETE')
  })
})

describe('overview', () => {
  it('offers the embed code, including the pinned version with SRI', async () => {
    const { user } = renderApp(NOTES)
    expect(await screen.findByText(/<kite-comments site="7f3c2a1e/)).toBeInTheDocument()
    await user.click(await screen.findByRole('tab', { name: 'Pinned version' }))
    expect(await screen.findByText(/integrity="sha384-/)).toBeInTheDocument()
  })

  it('tells the owner of an unverified site to verify it', async () => {
    renderApp(`/console/sites/${SITE_IDS.fresh}`)
    expect(await screen.findByText('One more step: verify your site')).toBeInTheDocument()
    expect(screen.getByRole('link', { name: 'Verify now' })).toHaveAttribute(
      'href',
      `/console/sites/${SITE_IDS.fresh}/verify`
    )
  })
})

describe('site settings and lifecycle', () => {
  it('saves changed settings only, with guests fixed off', async () => {
    const { user, writes } = renderApp(`${NOTES}/settings`)
    const guests = await screen.findByRole('radiogroup', { name: 'Guest comments' })
    expect(within(guests).getByRole('radio', { name: 'Off' })).toBeDisabled()

    const members = screen.getByRole('radiogroup', { name: 'Comments from signed-in readers' })
    await user.click(within(members).getByRole('radio', { name: 'Moderate first' }))
    const words = screen.getByRole('textbox', { name: 'Blocked words' })
    await user.type(words, '{enter}spam')
    await user.click(screen.getByRole('button', { name: 'Save' }))
    expect(await screen.findByText('Settings saved')).toBeInTheDocument()
    expect(writes().at(-1)).toMatchObject({
      method: 'PATCH',
      path: `/api/v1/sites/${SITE_IDS.notes}`,
      body: { member_policy: 'review', blocked_words: ['casino', expect.any(String), 'spam'] },
    })
  })

  it('adds and removes an alias origin', async () => {
    const { user, writes } = renderApp(`${NOTES}/settings`)
    const input = await screen.findByLabelText('Add an alias origin')
    await user.type(input, 'https://blog.amigoer.com')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await screen.findByText('Added https://blog.amigoer.com. Remember to verify it.')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Remove https://blog.amigoer.com' }))
    await user.click(within(await screen.findByRole('alertdialog')).getByRole('button', { name: 'Remove' }))
    expect(await screen.findByText('Removed https://blog.amigoer.com')).toBeInTheDocument()
    expect(writes().map((c) => c.method)).toEqual(['POST', 'DELETE'])
  })

  it('reports an origin that belongs to another site', async () => {
    const { user } = renderApp(`${NOTES}/settings`)
    await user.type(await screen.findByLabelText('Add an alias origin'), 'https://lin.dev')
    await user.click(screen.getByRole('button', { name: 'Add' }))
    expect(await screen.findByText('That origin already belongs to another site.')).toBeInTheDocument()
  })

  it('deletes a site after typing its name, then restores it', async () => {
    const { user, writes, db } = renderApp(`${NOTES}/data`)
    await user.click(await screen.findByRole('button', { name: 'Delete site...' }))
    const dialog = await screen.findByRole('alertdialog')
    const confirm = within(dialog).getByRole('button', { name: 'Delete site' })
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByRole('textbox'), db.sites[0].name)
    await user.click(confirm)
    expect(await screen.findByText('Site deleted. You can restore it within 30 days.')).toBeInTheDocument()
    expect(await screen.findByText('This site is deleted')).toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: 'Restore site' }))
    expect(await screen.findByText('Site restored')).toBeInTheDocument()
    expect(writes().map((c) => [c.method, c.path])).toEqual([
      ['DELETE', `/api/v1/sites/${SITE_IDS.notes}`],
      ['POST', `/api/v1/sites/${SITE_IDS.notes}/restore`],
    ])
  })

  it('exports the site in the chosen format', async () => {
    const { user, calls } = renderApp(`${NOTES}/data`)
    await user.click(await screen.findByRole('radio', { name: 'Artrans' }))
    await user.click(screen.getByRole('button', { name: 'Export' }))
    expect(await screen.findByText('Download started')).toBeInTheDocument()
    expect(calls.at(-1)?.path).toBe(`/api/v1/sites/${SITE_IDS.notes}/export?format=artrans`)
  })
})

describe('platform admin', () => {
  it('approves a site waiting for review', async () => {
    const { user, writes } = renderApp('/console/admin')
    const list = await screen.findByRole('list', { name: 'Site review' })
    const row = within(list).getAllByRole('listitem').find((li) => li.textContent?.includes('rustweekly'))!
    await user.click(within(row).getByRole('button', { name: 'Approve' }))
    expect(await screen.findByText(/^Approved "/)).toBeInTheDocument()
    expect(writes().at(-1)).toMatchObject({
      method: 'POST',
      path: `/api/v1/admin/sites/${SITE_IDS.rust}/approve`,
    })
  })

  it('is not available to other readers', async () => {
    renderApp('/console/admin', { setup: (db) => (db.me.is_platform_admin = false) })
    expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument()
    expect(screen.queryByRole('link', { name: 'Site review' })).not.toBeInTheDocument()
  })
})
