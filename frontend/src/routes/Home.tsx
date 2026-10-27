import { useQuery } from '@tanstack/react-query'
import { Database, Plus } from 'lucide-react'
import { Navigate } from 'react-router'
import { useUI } from '../app/ui'
import { bucketPath } from '../components/Dialogs'
import { Button, EmptyState, Spinner } from '../components/ui'
import { api, keys } from '../lib/api'

/** Opens the configured default bucket, or the first one there is. */
export function Home() {
  const ui = useUI()
  const buckets = useQuery({ queryKey: keys.buckets, queryFn: api.buckets })
  const info = useQuery({ queryKey: keys.info, queryFn: api.info })

  if (buckets.isPending || info.isPending) {
    return (
      <div className="grid flex-1 place-items-center">
        <Spinner />
      </div>
    )
  }
  const names = buckets.data?.map((b) => b.name) ?? []
  const target = names.includes(info.data?.defaultBucket ?? '') ? info.data!.defaultBucket : names[0]
  if (target) return <Navigate to={bucketPath(target)} replace />

  return (
    <EmptyState
      icon={<Database className="size-4" />}
      title="No buckets yet"
      description="Create one to start storing files, then point AWS_BUCKET at it."
      action={
        <Button variant="primary" onClick={() => ui.openDialog({ type: 'createBucket' })}>
          <Plus className="size-3.5" /> New bucket
        </Button>
      }
    />
  )
}
