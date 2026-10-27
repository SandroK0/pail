import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { createBrowserRouter } from 'react-router'
import { RouterProvider } from 'react-router/dom'
import { applyTheme } from './app/theme'
import './index.css'
import { AppProviders, newQueryClient, routes } from './router'

// Apply the saved theme before first paint.
try {
  applyTheme((localStorage.getItem('pail:theme') as 'light' | 'dark' | 'system' | null) ?? 'system')
} catch {
  applyTheme('system')
}

const router = createBrowserRouter(routes, { basename: '/_ui' })
const client = newQueryClient()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <AppProviders client={client}>
      <RouterProvider router={router} />
    </AppProviders>
  </StrictMode>,
)
