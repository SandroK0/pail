import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { fakeApi } from '../test/fakeApi'
import { location, renderApp } from '../test/render'

const seed = {
  laravel: {
    'readme.md': '# Hello',
    'config.json': '{"a":1}',
    'avatars/': '',
    'avatars/me.png': 'png-bytes',
    'avatars/2026/you.png': 'png',
    'invoices/INV-001.pdf': 'pdf',
  },
  media: {},
}

const rowNames = () =>
  screen
    .getAllByRole('row')
    .slice(1)
    .map((r) => within(r).getAllByRole('gridcell')[1].textContent)

const focusedName = () =>
  screen
    .getAllByRole('row')
    .find((r) => r.dataset.focused)
    ?.querySelectorAll('[role=gridcell]')[1].textContent

describe('BucketView', () => {
  it('lists folders first, then files', async () => {
    fakeApi(seed)
    renderApp('/b/laravel')
    await screen.findByText('readme.md')
    expect(rowNames()).toEqual(['avatars', 'invoices', 'config.json', 'readme.md'])
    expect(screen.getByText('4 items')).toBeInTheDocument()
  })

  it('opens folders, shows breadcrumbs and goes back up with Backspace', async () => {
    fakeApi(seed)
    const { router, user } = renderApp('/b/laravel')
    await user.dblClick(await screen.findByText('avatars'))
    await screen.findByText('me.png')
    expect(location(router)).toBe('/b/laravel/avatars')
    // The folder's own marker object is not listed.
    expect(rowNames()).toEqual(['2026', 'me.png'])
    const crumbs = screen.getByRole('navigation', { name: 'Breadcrumb' })
    expect(within(crumbs).getByText('avatars')).toBeInTheDocument()

    await user.keyboard('{Backspace}')
    await screen.findByText('readme.md')
    expect(location(router)).toBe('/b/laravel')
  })

  it('moves the cursor with j/k and opens with Enter', async () => {
    fakeApi(seed)
    const { router, user } = renderApp('/b/laravel')
    await screen.findByText('readme.md')
    await user.keyboard('j')
    expect(focusedName()).toBe('avatars')
    await user.keyboard('jj')
    expect(focusedName()).toBe('config.json')
    await user.keyboard('k')
    expect(focusedName()).toBe('invoices')
    await user.keyboard('{Enter}')
    await screen.findByText('INV-001.pdf')
    expect(location(router)).toBe('/b/laravel/invoices')
  })

  it('previews a file with details, and closes with Escape', async () => {
    fakeApi(seed)
    const { router, user } = renderApp('/b/laravel')
    await user.click(await screen.findByText('config.json'))
    const pane = await screen.findByRole('complementary', { name: 'Preview' })
    expect(location(router)).toBe('/b/laravel?preview=config.json')
    // JSON is pretty-printed.
    await within(pane).findByText('"a": 1')
    expect(within(pane).getByText('application/json')).toBeInTheDocument()
    expect(within(pane).getByRole('switch', { name: 'Public' })).toHaveAttribute('aria-checked', 'false')

    await user.keyboard('{Escape}')
    await waitFor(() => expect(screen.queryByRole('complementary', { name: 'Preview' })).not.toBeInTheDocument())
  })

  it('toggles visibility from the preview', async () => {
    const backend = fakeApi(seed)
    const { user } = renderApp('/b/laravel?preview=readme.md')
    const toggle = await screen.findByRole('switch', { name: 'Public' })
    await user.click(toggle)
    await waitFor(() => expect(toggle).toHaveAttribute('aria-checked', 'true'))
    expect(backend.buckets.get('laravel')!.get('readme.md')!.acl).toBe('public-read')
  })

  it('selects with x and deletes with confirmation', async () => {
    const backend = fakeApi(seed)
    const { user } = renderApp('/b/laravel')
    await screen.findByText('readme.md')
    await user.keyboard('jx') // select "avatars"
    await user.keyboard('jjjx') // select "readme.md"
    expect(screen.getByText('2 selected')).toBeInTheDocument()

    await user.keyboard('{Control>}{Backspace}{/Control}')
    const dialog = await screen.findByRole('dialog', { name: 'Delete 2 items?' })
    expect(within(dialog).getByText(/Folders are deleted with everything inside them/)).toBeInTheDocument()
    await user.click(within(dialog).getByRole('button', { name: 'Delete' }))

    await waitFor(() => expect(screen.queryByText('readme.md')).not.toBeInTheDocument())
    expect(backend.called('POST', '/_api/buckets/laravel/delete')[0].body).toEqual({
      keys: ['readme.md'],
      prefixes: ['avatars/'],
    })
    expect([...backend.buckets.get('laravel')!.keys()].sort()).toEqual(['config.json', 'invoices/INV-001.pdf'])
  })

  it('supports shift-click range selection and select all', async () => {
    fakeApi(seed)
    const { user } = renderApp('/b/laravel')
    await user.click(await screen.findByText('avatars'))
    await user.keyboard('{Shift>}')
    await user.click(screen.getByText('config.json'))
    await user.keyboard('{/Shift}')
    expect(screen.getByText('3 selected')).toBeInTheDocument()
    await user.keyboard('{Escape}')
    expect(screen.queryByText(/selected/)).not.toBeInTheDocument()
    await user.keyboard('{Control>}a{/Control}')
    expect(screen.getByText('4 selected')).toBeInTheDocument()
  })

  it('filters by name with /', async () => {
    fakeApi(seed)
    const { user } = renderApp('/b/laravel')
    await screen.findByText('readme.md')
    await user.keyboard('/')
    const input = await screen.findByLabelText('Filter by name')
    await waitFor(() => expect(input).toHaveFocus())
    await user.keyboard('ME')
    expect(rowNames()).toEqual(['readme.md'])
    await user.keyboard('{Escape}')
    expect(rowNames()).toHaveLength(4)
  })

  it('sorts by column', async () => {
    fakeApi(seed)
    const { user } = renderApp('/b/laravel')
    await screen.findByText('readme.md')
    await user.click(screen.getByRole('button', { name: 'Name' }))
    expect(rowNames()).toEqual(['invoices', 'avatars', 'readme.md', 'config.json'])
    await user.click(screen.getByRole('button', { name: 'Size' }))
    expect(rowNames()).toEqual(['avatars', 'invoices', 'config.json', 'readme.md'])
  })

  it('creates a folder with n', async () => {
    const backend = fakeApi(seed)
    const { user } = renderApp('/b/laravel/avatars')
    await screen.findByText('me.png')
    await user.keyboard('n')
    const dialog = await screen.findByRole('dialog', { name: 'New folder' })
    await user.type(within(dialog).getByLabelText('Folder name'), 'thumbs')
    await user.click(within(dialog).getByRole('button', { name: 'Create folder' }))
    await screen.findByText('thumbs')
    expect(backend.called('POST', '/_api/buckets/laravel/folders')[0].body).toEqual({ prefix: 'avatars/thumbs' })
  })

  it('renames with r', async () => {
    const backend = fakeApi(seed)
    const { user } = renderApp('/b/laravel')
    await screen.findByText('readme.md')
    await user.keyboard('jjjj') // readme.md
    await user.keyboard('r')
    const input = await screen.findByLabelText('New key')
    await user.clear(input)
    await user.type(input, 'README.md')
    await user.keyboard('{Enter}')
    await screen.findByText('README.md')
    expect(backend.called('POST', '/_api/buckets/laravel/move')[0].body).toEqual({ from: 'readme.md', to: 'README.md' })
  })

  it('generates presigned URLs', async () => {
    fakeApi(seed)
    const { user } = renderApp('/b/laravel?preview=readme.md')
    await user.click(await screen.findByRole('button', { name: /Presign/ }))
    const dialog = await screen.findByRole('dialog', { name: 'Presigned URL' })
    await user.selectOptions(within(dialog).getByLabelText('Expires in'), '3600')
    await user.click(within(dialog).getByRole('button', { name: 'Generate' }))
    await within(dialog).findByText(/X-Amz-Expires=3600/)

    await user.click(within(dialog).getByRole('radio', { name: 'Upload (PUT)' }))
    await user.click(within(dialog).getByRole('button', { name: 'Generate' }))
    await within(dialog).findByText(/curl -X PUT/)
  })

  it('shows an empty state with a Laravel hint', async () => {
    fakeApi(seed)
    renderApp('/b/media')
    expect(await screen.findByText('This bucket is empty')).toBeInTheDocument()
    expect(screen.getByText("Storage::put('hello.txt', 'Hello!');")).toBeInTheDocument()
  })

  it('reports a missing bucket', async () => {
    fakeApi(seed)
    renderApp('/b/ghost')
    expect(await screen.findByText('Bucket “ghost” not found')).toBeInTheDocument()
  })

  it('shows a drop overlay while dragging files', async () => {
    fakeApi(seed)
    renderApp('/b/laravel')
    await screen.findByText('readme.md')
    const area = screen.getByRole('grid').closest('section')!
    act(() => {
      fireEvent.dragEnter(area, { dataTransfer: { types: ['Files'] } })
    })
    expect(screen.getByText('Drop to upload to laravel/')).toBeInTheDocument()
    act(() => {
      fireEvent.dragLeave(area)
    })
    expect(screen.queryByText(/Drop to upload/)).not.toBeInTheDocument()
  })
})
