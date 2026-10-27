import * as Dialog from '@radix-ui/react-dialog'
import * as RadixTooltip from '@radix-ui/react-tooltip'
import {
  FileArchive,
  FileAudio,
  FileCode,
  FileImage,
  FileText,
  FileVideo,
  File as FileGeneric,
  Folder,
  LoaderCircle,
  X,
} from 'lucide-react'
import { forwardRef } from 'react'
import type { ButtonHTMLAttributes, InputHTMLAttributes, ReactNode } from 'react'
import { fileKind } from '../lib/format'
import type { FileKind } from '../lib/format'

export function cx(...classes: (string | false | null | undefined)[]): string {
  return classes.filter(Boolean).join(' ')
}

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'sm' | 'md'
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = 'secondary', size = 'md', className, type = 'button', ...props },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={cx(
        'inline-flex shrink-0 cursor-pointer items-center justify-center gap-1.5 rounded-md font-medium whitespace-nowrap transition-colors duration-100 select-none disabled:cursor-default disabled:opacity-50',
        size === 'sm' ? 'h-6 px-2 text-xs' : 'h-7 px-2.5',
        variant === 'primary' && 'bg-accent text-white enabled:hover:bg-accent-hover',
        variant === 'secondary' && 'border border-line-strong bg-raised text-fg enabled:hover:bg-hover',
        variant === 'ghost' && 'text-muted enabled:hover:bg-hover enabled:hover:text-fg',
        variant === 'danger' && 'bg-danger text-white enabled:hover:brightness-110',
        className,
      )}
      {...props}
    />
  )
})

export const IconButton = forwardRef<HTMLButtonElement, ButtonProps & { label: string }>(function IconButton(
  { label, className, ...props },
  ref,
) {
  return <Button ref={ref} variant="ghost" aria-label={label} className={cx('w-7 px-0', className)} {...props} />
})

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input(
  { className, ...props },
  ref,
) {
  return (
    <input
      ref={ref}
      className={cx(
        'h-8 w-full rounded-md border border-line-strong bg-panel px-2.5 text-fg transition-colors placeholder:text-faint focus:border-accent focus:outline-none',
        className,
      )}
      {...props}
    />
  )
})

export function Kbd({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <kbd
      className={cx(
        'inline-flex h-[18px] min-w-[18px] items-center justify-center rounded border border-line-strong bg-raised px-1 font-sans text-[11px] leading-none font-medium text-muted',
        className,
      )}
    >
      {children}
    </kbd>
  )
}

export function Tooltip({ label, shortcut, children }: { label: string; shortcut?: string[]; children: ReactNode }) {
  return (
    <RadixTooltip.Root>
      <RadixTooltip.Trigger asChild>{children}</RadixTooltip.Trigger>
      <RadixTooltip.Portal>
        <RadixTooltip.Content
          sideOffset={6}
          className="z-50 flex animate-fade-in items-center gap-2 rounded-md border border-line bg-raised px-2 py-1 text-xs text-fg shadow-pop"
        >
          {label}
          {shortcut && (
            <span className="flex gap-0.5">
              {shortcut.map((k) => (
                <Kbd key={k}>{k}</Kbd>
              ))}
            </span>
          )}
        </RadixTooltip.Content>
      </RadixTooltip.Portal>
    </RadixTooltip.Root>
  )
}

export function Spinner({ className }: { className?: string }) {
  return <LoaderCircle aria-label="Loading" className={cx('size-3.5 animate-spin text-muted', className)} />
}

export function Badge({
  children,
  tone = 'neutral',
  className,
}: {
  children: ReactNode
  tone?: 'neutral' | 'accent' | 'success' | 'danger' | 'warning'
  className?: string
}) {
  return (
    <span
      className={cx(
        'inline-flex h-5 items-center gap-1 rounded px-1.5 text-[11px] font-medium whitespace-nowrap',
        tone === 'neutral' && 'bg-raised text-muted ring-1 ring-line ring-inset',
        tone === 'accent' && 'bg-accent-soft text-accent',
        tone === 'success' && 'bg-success/12 text-success',
        tone === 'danger' && 'bg-danger/12 text-danger',
        tone === 'warning' && 'bg-warning/12 text-warning',
        className,
      )}
    >
      {children}
    </span>
  )
}

export function EmptyState({
  icon,
  title,
  description,
  action,
}: {
  icon: ReactNode
  title: string
  description?: ReactNode
  action?: ReactNode
}) {
  return (
    <div className="flex flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
      <div className="grid size-10 place-items-center rounded-lg border border-line bg-raised text-muted">{icon}</div>
      <div className="space-y-1">
        <p className="font-medium text-fg">{title}</p>
        {description && <div className="max-w-sm text-muted">{description}</div>}
      </div>
      {action}
    </div>
  )
}

export function Modal({
  open,
  onOpenChange,
  title,
  description,
  children,
  footer,
  className,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  description?: ReactNode
  children?: ReactNode
  footer?: ReactNode
  className?: string
}) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 animate-fade-in bg-overlay" />
        <Dialog.Content
          className={cx(
            'fixed top-[18vh] left-1/2 z-50 w-[calc(100vw-32px)] max-w-md -translate-x-1/2 animate-pop-in rounded-xl border border-line bg-panel shadow-pop focus:outline-none',
            className,
          )}
        >
          <div className="flex items-start justify-between gap-4 px-4 pt-4">
            <div className="space-y-1">
              <Dialog.Title className="text-sm font-medium">{title}</Dialog.Title>
              {description ? (
                <Dialog.Description className="text-muted">{description}</Dialog.Description>
              ) : (
                <Dialog.Description className="sr-only">{title}</Dialog.Description>
              )}
            </div>
            <Dialog.Close asChild>
              <IconButton label="Close" className="-mt-1 -mr-1.5 size-6">
                <X className="size-3.5" />
              </IconButton>
            </Dialog.Close>
          </div>
          {children && <div className="px-4 pt-4">{children}</div>}
          {footer && <div className="mt-4 flex justify-end gap-2 border-t border-line px-4 py-3">{footer}</div>}
          {!footer && <div className="h-4" />}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

const kindIcons: Record<FileKind, typeof FileGeneric> = {
  image: FileImage,
  video: FileVideo,
  audio: FileAudio,
  pdf: FileText,
  text: FileText,
  code: FileCode,
  archive: FileArchive,
  other: FileGeneric,
}

const kindColors: Record<FileKind, string> = {
  image: 'text-[#b78de8]',
  video: 'text-[#e8889a]',
  audio: 'text-[#e8b45e]',
  pdf: 'text-[#eb6a5e]',
  text: 'text-muted',
  code: 'text-[#5fb3d9]',
  archive: 'text-[#c9a46b]',
  other: 'text-muted',
}

export function ObjectIcon({ name, contentType, className }: { name: string; contentType?: string; className?: string }) {
  const kind = fileKind(name, contentType)
  const Icon = kindIcons[kind]
  return <Icon aria-hidden className={cx('size-4 shrink-0', kindColors[kind], className)} strokeWidth={1.75} />
}

export function FolderIcon({ className }: { className?: string }) {
  return (
    <Folder aria-hidden className={cx('size-4 shrink-0 fill-accent/25 text-accent', className)} strokeWidth={1.75} />
  )
}
