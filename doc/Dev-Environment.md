# Development Environment Guide

## Current Local Check (2026-04-18)
- Go: `go1.24.1`
- Rust: `rustc 1.90.0`, `cargo 1.90.0`
- Node: `v22.22.0`
- npm: `10.9.4`
- Docker: `29.1.5`, Compose `v5.0.1`
- Global `tsc`: not installed

## Recommended TypeScript Setup

### Option A (Recommended): project-local TypeScript
Use local dev dependency so every teammate uses the same compiler version.

```bash
cd apps/worker-ts
npm install -D typescript @types/node
npx tsc -p tsconfig.json --noEmit
```

### Option B: global TypeScript compiler
Useful for quick checks in any directory.

```bash
npm install -g typescript

tsc -v
```

## Why this repo can run without global tsc
`worker-ts` currently uses Node 22 `--experimental-strip-types` to run `.ts` directly for MVP speed.

For production and CI, still recommend Option A and run strict type checks with `npx tsc`.

## Optional VSCode Extensions
- Go: `golang.go`
- Rust: `rust-lang.rust-analyzer`
- TypeScript: built-in + `ms-vscode.vscode-typescript-next`
