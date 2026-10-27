import { act, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { FakeEventSource, fakeApi } from '../test/fakeApi'
import { location, renderApp } from '../test/render'

describe('Home', () => {
  it('opens the default bucket', async () => {
    fakeApi({ alpha: {}, laravel: { 'a.txt': 'x' } })
    const { router } = renderApp('/')
    await screen.findByText('a.txt')
    expect(location(router)).toBe('/b/laravel')
  })

  it('falls back to the first bucket', async () => {
    fakeApi({ alpha: {}, beta: {} }, { defaultBucket: 'missing' })
    const { router } = renderApp('/')
    await waitFor(() => expect(location(router)).toBe('/b/alpha'))
  })

  it('offers to create a bucket when there are none', async () => {
    fakeApi({})
    const { user } = renderApp('/')
    await user.click(await screen.findByRole('button', { name: 'New bucket' }))
    expect(await screen.findByRole('dialog', { name: 'New bucket' })).toBeInTheDocument()
  })

  it('shows a not-found page for unknown routes', async () => {
    fakeApi()
    renderApp('/nope')
    expect(await screen.findByText('Page not found')).toBeInTheDocument()
  })
})

describe('Sidebar', () => {
  it('lists buckets with object counts and highlights the current one', async () => {
    fakeApi({ laravel: { a: '1', b: '2' }, media: {} })
    renderApp('/b/media')
    const sidebar = screen.getByRole('complementary', { name: 'Sidebar' })
    const link = await within(sidebar).findByRole('link', { name: /laravel/ })
    expect(link).toHaveTextContent('2')
    expect(within(sidebar).getByRole('link', { name: /media/ })).toHaveClass('bg-active')
  })

  it('creates a bucket, validating the name as you type', async () => {
    const backend = fakeApi({ laravel: {} })
    const { router, user } = renderApp('/b/laravel')
    await user.click(await screen.findByRole('button', { name: 'New bucket' }))
    const dialog = await screen.findByRole('dialog', { name: 'New bucket' })
    const input = within(dialog).getByLabelText('Bucket name')
    const submit = within(dialog).getByRole('button', { name: 'Create bucket' })

    await user.type(input, 'ab')
    expect(within(dialog).getByText('Use 3–63 characters')).toBeInTheDocument()
    expect(submit).toBeDisabled()

    await user.type(input, 'c-Uploads') // uppercased input is lowercased
    expect(input).toHaveValue('abc-uploads')
    await user.click(submit)
    await waitFor(() => expect(location(router)).toBe('/b/abc-uploads'))
    expect(backend.buckets.has('abc-uploads')).toBe(true)
  })

  it('shows server errors when creating a bucket', async () => {
    fakeApi({ laravel: {}, taken: {} })
    const { user } = renderApp('/b/laravel')
    await user.click(await screen.findByRole('button', { name: 'New bucket' }))
    const dialog = await screen.findByRole('dialog', { name: 'New bucket' })
    await user.type(within(dialog).getByLabelText('Bucket name'), 'taken{Enter}')
    expect(await within(dialog).findByText('A bucket with that name already exists')).toBeInTheDocument()
  })

  it('deletes a bucket after typing its name', async () => {
    const backend = fakeApi({ laravel: {}, junk: { 'a.txt': 'x' } })
    const { router, user } = renderApp('/b/junk')
    await user.click(await screen.findByRole('button', { name: 'junk options' }))
    await user.click(await screen.findByRole('menuitem', { name: /Delete bucket/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Delete junk?' })
    const confirm = within(dialog).getByRole('button', { name: 'Delete bucket' })
    expect(confirm).toBeDisabled()
    await user.type(within(dialog).getByLabelText('Confirm bucket name'), 'junk')
    await user.click(confirm)
    await waitFor(() => expect(backend.buckets.has('junk')).toBe(false))
    await waitFor(() => expect(location(router)).toBe('/b/laravel'))
  })

  it('toggles the theme', async () => {
    fakeApi()
    const { user } = renderApp('/b/laravel')
    const before = document.documentElement.classList.contains('dark')
    await user.click(screen.getByRole('button', { name: 'Toggle theme' }))
    expect(document.documentElement.classList.contains('dark')).toBe(!before)
    expect(localStorage.getItem('pail:theme')).toBe(before ? 'light' : 'dark')
  })

  it('opens the shortcuts sheet with ?', async () => {
    fakeApi()
    const { user } = renderApp('/b/laravel')
    await screen.findByText('This bucket is empty')
    await user.keyboard('?')
    const dialog = await screen.findByRole('dialog', { name: 'Keyboard shortcuts' })
    expect(within(dialog).getByText('Go up a folder')).toBeInTheDocument()
  })
})

describe('Command palette', () => {
  it('opens with mod+k and jumps to a bucket', async () => {
    fakeApi({ laravel: {}, media: { 'x.png': 'p' } })
    const { router, user } = renderApp('/b/laravel')
    await screen.findByText('This bucket is empty')
    await user.keyboard('{Control>}k{/Control}')
    const input = await screen.findByPlaceholderText(/Search laravel or type a command/)
    await user.type(input, 'media')
    await user.keyboard('{Enter}')
    await waitFor(() => expect(location(router)).toBe('/b/media'))
    expect(screen.queryByPlaceholderText(/type a command/)).not.toBeInTheDocument()
  })

  it('searches objects in the current bucket and opens the preview', async () => {
    fakeApi({ laravel: { 'invoices/2026/INV-042.pdf': 'pdf', 'readme.md': 'x' } })
    const { router, user } = renderApp('/b/laravel')
    await screen.findByText('readme.md')
    await user.keyboard('{Control>}k{/Control}')
    await user.type(await screen.findByRole('combobox'), 'inv-04')
    const option = await screen.findByRole('option', { name: /invoices\/2026\/INV-042\.pdf/ })
    await user.click(option)
    await waitFor(() => expect(location(router)).toBe('/b/laravel/invoices/2026?preview=invoices/2026/INV-042.pdf'))
    expect(await screen.findByRole('complementary', { name: 'Preview' })).toBeInTheDocument()
  })

  it('runs actions such as new folder', async () => {
    fakeApi({ laravel: {} })
    const { user } = renderApp('/b/laravel')
    await screen.findByText('This bucket is empty')
    await user.keyboard('{Control>}k{/Control}')
    await user.type(await screen.findByRole('combobox'), 'new folder')
    await user.keyboard('{Enter}')
    expect(await screen.findByRole('dialog', { name: 'New folder' })).toBeInTheDocument()
  })
})

describe('Activity', () => {
  it('streams requests live and flags errors in the sidebar', async () => {
    fakeApi()
    const { user } = renderApp('/b/laravel')
    await screen.findByText('This bucket is empty')
    const es = FakeEventSource.latest()
    expect(es.url).toBe('/_api/events')
    act(() => es.open())
    act(() => {
      es.emit({ op: 'PutObject', method: 'PUT', key: 'avatars/me.png', status: 200 })
      es.emit({ op: 'GetObject', key: 'missing.txt', status: 404, errorCode: 'NoSuchKey', errorMessage: 'nope' })
    })
    expect(await screen.findByLabelText('1 new errors')).toBeInTheDocument()

    await user.click(screen.getByRole('link', { name: /Activity/ }))
    expect(await screen.findByText('avatars/me.png')).toBeInTheDocument()
    expect(screen.getByText('NoSuchKey')).toBeInTheDocument()
    expect(screen.getAllByText('aws-sdk-php 3.300.0')).toHaveLength(2)
    expect(screen.getByText('Live')).toBeInTheDocument()
    // Visiting the log marks errors as seen.
    await waitFor(() => expect(screen.queryByLabelText(/new errors/)).not.toBeInTheDocument())
  })

  it('filters, searches and shows request details', async () => {
    fakeApi()
    const { user } = renderApp('/activity')
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit({ op: 'PutObject', method: 'PUT', key: 'a.txt', status: 200 })
      es.emit({ op: 'HeadObject', method: 'HEAD', key: 'b.txt', status: 404, errorCode: 'NoSuchKey' })
      es.emit({ op: 'ListObjectsV2', method: 'GET', key: '', status: 200 })
    })
    await screen.findByText('a.txt')
    expect(screen.getByText('3 requests')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: 'errors' }))
    expect(screen.getByText('1 requests')).toBeInTheDocument()
    await user.click(screen.getByRole('tab', { name: 'writes' }))
    expect(screen.getByText('1 requests')).toBeInTheDocument()
    expect(screen.getByText('a.txt')).toBeInTheDocument()

    await user.click(screen.getByRole('tab', { name: 'all' }))
    await user.type(screen.getByLabelText('Search requests'), 'listobjects')
    expect(screen.getByText('1 requests')).toBeInTheDocument()
    await user.clear(screen.getByLabelText('Search requests'))

    await user.click(screen.getByText('a.txt'))
    const detail = screen.getByRole('complementary', { name: 'Request detail' })
    expect(within(detail).getByRole('heading', { name: 'PutObject' })).toBeInTheDocument()
    expect(within(detail).getByRole('link', { name: 'Open object →' })).toHaveAttribute('href', '/b/laravel?preview=a.txt')
  })

  it('filters by operation type', async () => {
    fakeApi()
    const { user } = renderApp('/activity')
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit({ op: 'PutObject', method: 'PUT', key: 'put.txt' })
      es.emit({ op: 'GetObject', key: 'get-1.txt' })
      es.emit({ op: 'GetObject', key: 'get-2.txt' })
      es.emit({ op: 'HeadObject', method: 'HEAD', key: 'head.txt' })
    })
    await screen.findByText('put.txt')

    const trigger = screen.getByRole('button', { name: 'Filter by operation' })
    expect(trigger).toHaveTextContent('All operations')
    await user.click(trigger)
    const menu = await screen.findByRole('menu')
    // Most frequent first, with counts.
    const items = within(menu).getAllByRole('menuitemcheckbox')
    expect(items.map((i) => i.textContent)).toEqual(['GetObject2', 'HeadObject1', 'PutObject1'])

    await user.click(items[0])
    // The menu stays open for picking several.
    await user.click(within(menu).getByRole('menuitemcheckbox', { name: /PutObject/ }))
    expect(screen.getByText('3 requests')).toBeInTheDocument()
    expect(screen.queryByText('head.txt')).not.toBeInTheDocument()
    expect(trigger).toHaveTextContent('2 operations')

    await user.click(within(menu).getByRole('menuitemcheckbox', { name: /PutObject/ }))
    expect(trigger).toHaveTextContent('GetObject')
    expect(screen.getByText('2 requests')).toBeInTheDocument()

    await user.click(within(menu).getByRole('menuitem', { name: 'Show all operations' }))
    expect(trigger).toHaveTextContent('All operations')
    expect(screen.getByText('4 requests')).toBeInTheDocument()
  })

  it('pauses and resumes', async () => {
    fakeApi()
    const { user } = renderApp('/activity')
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit({ key: 'first.txt' })
    })
    await screen.findByText('first.txt')
    await user.click(screen.getByRole('button', { name: 'Pause' }))
    act(() => es.emit({ key: 'second.txt' }))
    await screen.findByRole('button', { name: 'Resume (1 new)' })
    expect(screen.queryByText('second.txt')).not.toBeInTheDocument()
    await user.click(screen.getByRole('button', { name: /Resume/ }))
    expect(screen.getByText('second.txt')).toBeInTheDocument()
  })

  it('shows reconnecting state and replaces the log on reconnect', async () => {
    fakeApi()
    renderApp('/activity')
    const es = FakeEventSource.latest()
    act(() => {
      es.open()
      es.emit({ key: 'old.txt' })
    })
    await screen.findByText('old.txt')
    act(() => es.fail())
    expect(screen.getByText('Reconnecting…')).toBeInTheDocument()
    act(() => es.open()) // server replays its backlog after this
    await waitFor(() => expect(screen.queryByText('old.txt')).not.toBeInTheDocument())
  })
})

describe('Setup', () => {
  it('shows a ready-to-paste .env and runs the connection test', async () => {
    fakeApi({ laravel: {}, media: {} }, { endpoint: 'http://localhost:9000', verifyAuth: true })
    const { user } = renderApp('/setup')
    expect(await screen.findByText(/AWS_ENDPOINT=http:\/\/localhost:9000/)).toBeInTheDocument()
    expect(screen.getByText(/AWS_URL=http:\/\/localhost:9000\/laravel/)).toBeInTheDocument()
    expect(screen.getByText(/Verified/)).toBeInTheDocument()

    await user.selectOptions(screen.getByLabelText('Bucket'), 'media')
    expect(screen.getByText(/AWS_BUCKET=media/)).toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /Run test against media/ }))
    for (const step of ['HeadBucket', 'PutObject', 'GetObject', 'DeleteObject']) {
      expect(await screen.findByText(step)).toBeInTheDocument()
    }
  })
})
