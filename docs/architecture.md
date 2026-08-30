# Architecture

SiteWatch deliberately uses one process and one SQLite file.

- `cmd/sitewatch` owns Cobra commands, terminal output, the HTTP API, and Go-template UI.
- `internal/app` runs the fetch → extract → compare → persist pipeline.
- `internal/fetcher` owns request safety, limits, retries, redirects, and conditional headers.
- `internal/crawler` performs bounded same-host discovery using links, robots.txt, and sitemaps.
- `internal/normalize` canonicalizes text and URLs and removes known volatile/tracking data.
- `internal/extractor` turns HTML and JSON-LD into `snapshot.Page`.
- `internal/diff` emits deterministic scored `snapshot.Change` values.
- `internal/storage` uses `database/sql` and SQLite transactions directly.
- `internal/scheduler` runs due checks with bounded concurrency and per-monitor exclusion.
- `internal/notifier` contains the console and webhook implementations.

Interfaces exist only at the actual extension seam (`Notifier`). SQLite is concrete in the MVP; extracting storage behind an interface is deferred until a second implementation exists.

Snapshots contain the structured representation, metadata, and six fingerprints. Raw HTML is not retained. A technical-only response updates HTTP validators and check status without growing history.

The optional browser-rendering phase is deliberately absent from the default dependency graph. The extractor flags thin JavaScript shells so a future build-tagged renderer can be added without changing deterministic comparison behavior.
