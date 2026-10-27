import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import type { ReactNode } from 'react'
import type { RouteObject } from 'react-router'
import { EventsProvider } from './app/events'
import { UIProvider } from './app/ui'
import { UploadsProvider } from './app/uploads'
import { EmptyState } from './components/ui'
import { Activity } from './routes/Activity'
import { BucketView } from './routes/BucketView'
import { Home } from './routes/Home'
import { Layout } from './routes/Layout'
import { Setup } from './routes/Setup'

export const routes: RouteObject[] = [
  {
    path: '/',
    element: <Layout />,
    children: [
      { index: true, element: <Home /> },
      { path: 'b/:bucket/*', element: <BucketView /> },
      { path: 'activity', element: <Activity /> },
      { path: 'setup', element: <Setup /> },
      { path: '*', element: <EmptyState icon="?" title="Page not found" /> },
    ],
  },
]

export function newQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { staleTime: 5_000, refetchOnWindowFocus: true, retry: 1 },
    },
  })
}

/** Everything the routes need above the router. */
export function AppProviders({ client, children }: { client: QueryClient; children: ReactNode }) {
  return (
    <QueryClientProvider client={client}>
      <EventsProvider>
        <UploadsProvider>
          <UIProvider>{children}</UIProvider>
        </UploadsProvider>
      </EventsProvider>
    </QueryClientProvider>
  )
}
