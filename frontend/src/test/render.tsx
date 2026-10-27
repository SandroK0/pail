import { QueryClient } from '@tanstack/react-query'
import { render } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { createMemoryRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { AppProviders, routes } from '../router'

/** Renders the whole app at `path` with a fresh query cache. */
export function renderApp(path = '/') {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: 0 } } })
  const router = createMemoryRouter(routes, { initialEntries: [path] })
  const user = userEvent.setup()
  const utils = render(
    <AppProviders client={client}>
      <RouterProvider router={router} />
    </AppProviders>,
  )
  return { ...utils, router, client, user }
}

/** The current pathname plus search, for asserting navigation. */
export function location(router: ReturnType<typeof createMemoryRouter>): string {
  const { pathname, search } = router.state.location
  return decodeURIComponent(pathname + search)
}
