import { useMutation, useQuery } from '@tanstack/react-query'
import { CircleCheck, CircleX, Play, ShieldCheck, ShieldOff } from 'lucide-react'
import { useState } from 'react'
import type { ReactNode } from 'react'
import { CopyField } from '../components/Dialogs'
import { Badge, Button, Spinner } from '../components/ui'
import { api, keys } from '../lib/api'
import type { Info } from '../lib/api'
import { formatDuration } from '../lib/format'
import { PageHeader } from './Layout'

export function mcpCommand(info: Info): string {
  return `claude mcp add --transport http pail ${info.endpoint}/_mcp`
}

export function envBlock(info: Info, bucket: string): string {
  return [
    'FILESYSTEM_DISK=s3',
    `AWS_ACCESS_KEY_ID=${info.accessKey}`,
    `AWS_SECRET_ACCESS_KEY=${info.secretKey}`,
    `AWS_DEFAULT_REGION=${info.region}`,
    `AWS_BUCKET=${bucket}`,
    `AWS_ENDPOINT=${info.endpoint}`,
    'AWS_USE_PATH_STYLE_ENDPOINT=true',
    `AWS_URL=${info.endpoint}/${bucket}`,
  ].join('\n')
}

const diskConfig = `'s3' => [
    'driver' => 's3',
    'key' => env('AWS_ACCESS_KEY_ID'),
    'secret' => env('AWS_SECRET_ACCESS_KEY'),
    'region' => env('AWS_DEFAULT_REGION'),
    'bucket' => env('AWS_BUCKET'),
    'url' => env('AWS_URL'),
    'endpoint' => env('AWS_ENDPOINT'),
    'use_path_style_endpoint' => env('AWS_USE_PATH_STYLE_ENDPOINT', false),
    'throw' => true,
],`

const tinker = `Storage::put('hello.txt', 'Hello from Laravel');
Storage::get('hello.txt');
Storage::url('hello.txt');
Storage::temporaryUrl('hello.txt', now()->addMinutes(5));`

function Section({ title, description, children }: { title: string; description?: ReactNode; children: ReactNode }) {
  return (
    <section className="grid gap-4 border-b border-line py-6 md:grid-cols-[220px_minmax(0,1fr)]">
      <div>
        <h2 className="font-medium">{title}</h2>
        {description && <p className="mt-1 text-muted">{description}</p>}
      </div>
      <div className="min-w-0">{children}</div>
    </section>
  )
}

function Code({ children }: { children: string }) {
  return (
    <pre className="overflow-x-auto rounded-lg border border-line bg-raised p-3 font-mono text-xs leading-5 text-fg">{children}</pre>
  )
}

export function Setup() {
  const info = useQuery({ queryKey: keys.info, queryFn: api.info })
  const buckets = useQuery({ queryKey: keys.buckets, queryFn: api.buckets })
  const [chosen, setChosen] = useState<string | null>(null)
  const bucket = chosen ?? info.data?.defaultBucket ?? buckets.data?.[0]?.name ?? 'laravel'
  const test = useMutation({ mutationFn: () => api.selftest(bucket) })

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <PageHeader>
        <h1 className="font-medium">Laravel setup</h1>
      </PageHeader>
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto max-w-4xl px-6">
          {info.isPending && <Spinner className="m-6" />}
          {info.data && (
            <>
              <Section title="Connection" description="Where your app should point.">
                <dl className="grid grid-cols-[120px_minmax(0,1fr)] gap-y-2">
                  <dt className="text-muted">Endpoint</dt>
                  <dd className="font-mono text-xs leading-5">{info.data.endpoint}</dd>
                  <dt className="text-muted">Region</dt>
                  <dd className="font-mono text-xs leading-5">{info.data.region}</dd>
                  <dt className="text-muted">Signatures</dt>
                  <dd>
                    {info.data.verifyAuth ? (
                      <Badge tone="success">
                        <ShieldCheck className="size-3" /> Verified (AUTH=verify)
                      </Badge>
                    ) : (
                      <Badge>
                        <ShieldOff className="size-3" /> Not checked (AUTH=off)
                      </Badge>
                    )}
                  </dd>
                </dl>
              </Section>

              <Section
                title=".env"
                description={
                  <>
                    Paste into your Laravel app. Requires <code className="font-mono text-xs">league/flysystem-aws-s3-v3</code>.
                  </>
                }
              >
                <div className="mb-2 flex items-center gap-2">
                  <label htmlFor="setup-bucket" className="text-muted">
                    Bucket
                  </label>
                  <select
                    id="setup-bucket"
                    value={bucket}
                    onChange={(e) => {
                      setChosen(e.target.value)
                      test.reset()
                    }}
                    className="h-7 rounded-md border border-line-strong bg-panel px-2 text-fg"
                  >
                    {(buckets.data ?? []).map((b) => (
                      <option key={b.name}>{b.name}</option>
                    ))}
                  </select>
                </div>
                <CopyField value={envBlock(info.data, bucket)} label="Copy .env" />
              </Section>

              <Section title="Test connection" description="Signs real S3 requests with these credentials, exactly as Laravel would.">
                <Button variant="primary" onClick={() => test.mutate()} disabled={test.isPending}>
                  {test.isPending ? <Spinner className="text-white" /> : <Play className="size-3" />}
                  Run test against {bucket}
                </Button>
                {test.data && (
                  <ul className="mt-3 divide-y divide-line rounded-lg border border-line">
                    {test.data.map((s) => (
                      <li key={s.name} className="flex h-9 items-center gap-2.5 px-3">
                        {s.ok ? <CircleCheck className="size-3.5 text-success" /> : <CircleX className="size-3.5 text-danger" />}
                        <span className="flex-1">{s.name}</span>
                        {s.error && <span className="text-danger">{s.error}</span>}
                        <span className="w-16 text-right text-xs text-faint tabular-nums">{formatDuration(s.durationMs)}</span>
                      </li>
                    ))}
                  </ul>
                )}
                {test.error && <p className="mt-2 text-danger">{test.error.message}</p>}
              </Section>

              <Section title="config/filesystems.php" description="Laravel's default s3 disk already reads these. Shown for reference.">
                <Code>{diskConfig}</Code>
              </Section>

              <Section title="Try it" description="In php artisan tinker:">
                <Code>{tinker}</Code>
              </Section>

              {info.data.mcp && (
                <Section
                  title="AI assistants (MCP)"
                  description="Let Claude Code or any MCP client look at your buckets, files and the request log."
                >
                  <CopyField value={mcpCommand(info.data)} label="Copy MCP command" />
                  <p className="mt-2 text-muted">
                    Other clients: add a streamable HTTP server at{' '}
                    <code className="font-mono text-xs text-fg">{info.data.endpoint}/_mcp</code>. Tools are read-only,
                    so you can ask things like “why did my last upload fail?” or “what's in avatars/?”.
                  </p>
                </Section>
              )}

              <Section title="Browser uploads" description="For direct uploads with temporaryUploadUrl().">
                <p className="text-muted">
                  CORS is open to every origin by default. Restrict it with{' '}
                  <code className="font-mono text-xs text-fg">CORS_ORIGINS=http://app.test</code>.
                </p>
              </Section>
            </>
          )}
        </div>
      </div>
    </div>
  )
}
