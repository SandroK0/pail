import { act, fireEvent, screen, waitFor, within } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { api } from '../lib/api'
import type { ObjectDetail } from '../lib/api'
import { fakeApi } from '../test/fakeApi'
import { renderApp } from '../test/render'
import { CONCURRENCY } from './uploads'

type Pending = {
  key: string
  progress?: (loaded: number, total: number) => void
  signal?: AbortSignal
  resolve: () => void
  reject: (e: Error) => void
}

/** Replaces api.upload with uploads the test finishes by hand. */
function controlledUploads() {
  const pending: Pending[] = []
  vi.spyOn(api, 'upload').mockImplementation(
    (_bucket, key, _file, progress, signal) =>
      new Promise<ObjectDetail>((resolve, reject) => {
        signal?.addEventListener('abort', () => reject(new Error('Cancelled')))
        pending.push({ key, progress, signal, resolve: () => resolve({} as ObjectDetail), reject })
      }),
  )
  return pending
}

const pick = (names: string[]) => {
  const input = screen.getByTestId('file-picker') as HTMLInputElement
  fireEvent.change(input, { target: { files: names.map((n) => new File(['data'], n, { type: 'text/plain' })) } })
}

describe('uploads', () => {
  it(`runs at most ${CONCURRENCY} at once and refreshes the listing`, async () => {
    const backend = fakeApi({ laravel: {} })
    const pending = controlledUploads()
    const { user } = renderApp('/b/laravel/docs')
    await screen.findByText('This folder is empty')

    await user.keyboard('u') // opens the picker for laravel/docs/
    pick(['a.txt', 'b.txt', 'c.txt', 'd.txt'])

    const tray = await screen.findByRole('region', { name: 'Uploads' })
    await waitFor(() => expect(pending).toHaveLength(CONCURRENCY))
    expect(pending.map((p) => p.key)).toEqual(['docs/a.txt', 'docs/b.txt', 'docs/c.txt'])
    expect(within(tray).getByText('Waiting…')).toBeInTheDocument()
    expect(within(tray).getByText(/Uploading 4 files/)).toBeInTheDocument()

    act(() => pending[0].progress?.(2, 4))
    expect(within(tray).getByText('2 B of 4 B')).toBeInTheDocument()

    // Finishing one starts the next, and the listing refetches.
    const listCalls = backend.called('GET', '/_api/buckets/laravel/objects').length
    backend.put('laravel', 'docs/a.txt', 'data')
    await act(async () => pending[0].resolve())
    await waitFor(() => expect(pending).toHaveLength(4))
    await within(await screen.findByRole('grid')).findByText('a.txt')
    expect(backend.called('GET', '/_api/buckets/laravel/objects').length).toBeGreaterThan(listCalls)

    await act(async () => {
      pending[1].resolve()
      pending[2].reject(new Error('Bucket not found'))
      pending[3].resolve()
    })
    await within(tray).findByText('1 upload failed')
    expect(within(tray).getByText('Bucket not found')).toBeInTheDocument()

    await user.click(within(tray).getByRole('button', { name: 'Clear' }))
    expect(screen.queryByRole('region', { name: 'Uploads' })).not.toBeInTheDocument()
  })

  it('cancels a running upload', async () => {
    fakeApi({ laravel: {} })
    const pending = controlledUploads()
    const { user } = renderApp('/b/laravel')
    await screen.findByText('This bucket is empty')
    await user.keyboard('u')
    pick(['big.bin'])
    const tray = await screen.findByRole('region', { name: 'Uploads' })
    await waitFor(() => expect(pending).toHaveLength(1))
    await user.click(within(tray).getByRole('button', { name: 'Cancel big.bin' }))
    expect(pending[0].signal?.aborted).toBe(true)
    expect(await within(tray).findByText('Cancelled')).toBeInTheDocument()
    expect(within(tray).getByText('Uploads complete')).toBeInTheDocument()
  })

  it('collapses the tray', async () => {
    fakeApi({ laravel: {} })
    controlledUploads()
    const { user } = renderApp('/b/laravel')
    await screen.findByText('This bucket is empty')
    await user.keyboard('u')
    pick(['a.txt'])
    const tray = await screen.findByRole('region', { name: 'Uploads' })
    await user.click(within(tray).getByRole('button', { name: 'Collapse uploads' }))
    expect(within(tray).queryByText('a.txt')).not.toBeInTheDocument()
  })
})
