handcoding this project

# pail

A small S3-compatible object store for local development: one Go binary, a
Linear-style web UI, and a live log of every S3 call your app makes. Works with
any S3 client that supports path-style addressing; Laravel gets a setup page.

## Run it

```bash
cd frontend && npm install && npm run build && cd ..
go run .
```

Open http://localhost:8080 for the UI. The `laravel` bucket is created on
startup.

## Point Laravel at it

```dotenv
FILESYSTEM_DISK=s3
AWS_ACCESS_KEY_ID=local
AWS_SECRET_ACCESS_KEY=localsecret
AWS_DEFAULT_REGION=us-east-1
AWS_BUCKET=laravel
AWS_ENDPOINT=http://localhost:8080
AWS_USE_PATH_STYLE_ENDPOINT=true
AWS_URL=http://localhost:8080/laravel
```

Requires `composer require league/flysystem-aws-s3-v3`. The **Laravel setup**
page in the UI shows this block for any bucket and can test the connection.

## Ask your AI assistant (MCP)

pail serves an MCP endpoint at `/_mcp`, so Claude Code (or any MCP client) can
look at your buckets, read files and inspect the live request log:

```bash
claude mcp add --transport http pail http://localhost:8080/_mcp
```

Then ask things like "why did my last upload fail?", "what's in
`avatars/`?" or "which requests are slow?". The tools are read-only:

| Tool | |
|---|---|
| `server_info` | endpoint, credentials, auth mode, the Laravel `.env` block |
| `list_buckets` | buckets with object counts and sizes |
| `list_objects` | one folder level, or everything under a prefix |
| `search_objects` | keys containing a string |
| `get_object` | size, type, ETag, visibility, metadata, URL |
| `read_object` | the start of a text file |
| `recent_requests` | the request log, filterable by errors, operation, bucket, key, time |
| `request_stats` | counts, errors and latency per operation, top error codes and clients |
| `presign_url` | a temporary GET or PUT URL |

Cross-site browser requests to `/_mcp` are rejected. Set `MCP=off` to disable it.

## Configuration

Environment variables (or `.env`):

| Variable | Default | |
|---|---|---|
| `ADDR` | `:8080` | listen address |
| `DATA_DIR` | `./data` | where objects and `meta.db` live |
| `PUBLIC_URL` | `http://localhost:<port>` | base for object URLs and presigned links |
| `ACCESS_KEY` / `SECRET_KEY` | `local` / `localsecret` | credentials |
| `REGION` | `us-east-1` | |
| `AUTH` | `off` | `verify` checks SigV4 signatures and blocks anonymous reads of private objects |
| `BUCKETS` | `laravel` | comma-separated buckets to create at startup |
| `AUTO_CREATE_BUCKETS` | `false` | create a bucket on first write to it |
| `CORS_ORIGINS` | `*` | allowed origins for browser uploads |
| `LOG_FILE` | `app.log` | `off` to log to stderr only |
| `MCP` | `on` | `off` disables the `/_mcp` endpoint |

Presigned URLs always expire, even with `AUTH=off`.

## Develop

```bash
go test -race ./...              # Go: store, SigV4, aws-chunked, S3 API (via aws-sdk-go-v2), admin API, MCP
cd frontend && npm test          # UI: Vitest + Testing Library
npm run dev                      # UI dev server on :5173/_ui/, proxies /_api to :8080
```

`compat/laravel` runs Laravel's own S3 disk against a running server:

```bash
cd compat/laravel && composer install && php run.php
```

## Layout

```
main.go                  wiring and startup
config/                  environment config
internal/store/          SQLite metadata + blob files; keys are never filesystem paths
internal/s3api/          S3 REST API (path-style), XML, multipart, ACLs, CORS
internal/sigv4/          SigV4 header and presigned-URL signing and verification
internal/awschunked/     aws-chunked body decoder
internal/events/         request log ring buffer and live subscribers
internal/adminapi/       JSON API for the UI (/_api)
internal/mcpserver/      read-only MCP tools for AI assistants (/_mcp)
internal/server/         one-port router: /_api, /_mcp, /_ui, everything else is S3
web/                     embeds the built UI (frontend builds into web/dist)
frontend/                React + Tailwind UI
```
