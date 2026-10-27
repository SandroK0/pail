import * as RadixTooltip from '@radix-ui/react-tooltip'
import { Outlet } from 'react-router'
import { Toaster } from 'sonner'
import { useTheme } from '../app/theme'
import { useUI } from '../app/ui'
import { CommandPalette } from '../components/CommandPalette'
import { Dialogs } from '../components/Dialogs'
import { Sidebar } from '../components/Sidebar'
import { UploadTray } from '../components/UploadTray'
import { useHotkeys } from '../lib/hotkeys'

export function Layout() {
  const theme = useTheme()
  const ui = useUI()
  const overlayOpen = ui.paletteOpen || ui.dialog !== null

  useHotkeys({
    'mod+k': () => ui.setPaletteOpen(!ui.paletteOpen),
    '?': () => !overlayOpen && ui.openDialog({ type: 'shortcuts' }),
  })

  return (
    <RadixTooltip.Provider delayDuration={400} skipDelayDuration={150}>
      <div className="flex h-full overflow-hidden">
        <Sidebar theme={theme} />
        <main className="flex min-w-0 flex-1 flex-col">
          <Outlet />
        </main>
      </div>
      <CommandPalette theme={theme} />
      <Dialogs />
      <UploadTray />
      <Toaster
        theme={theme.resolved}
        position="bottom-left"
        toastOptions={{
          className: '!bg-raised !border-line !text-fg !shadow-pop !rounded-lg !text-[13px] !font-sans',
        }}
      />
    </RadixTooltip.Provider>
  )
}

/** Header bar shared by every page. */
export function PageHeader({ children, actions }: { children: React.ReactNode; actions?: React.ReactNode }) {
  return (
    <header className="flex h-12 shrink-0 items-center gap-3 border-b border-line px-4">
      <div className="flex min-w-0 flex-1 items-center gap-1">{children}</div>
      {actions && <div className="flex items-center gap-1.5">{actions}</div>}
    </header>
  )
}
