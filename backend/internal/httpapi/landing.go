package httpapi

import "html/template"

type landingData struct {
	Version     string
	Environment string
	Phase       string
}

// landingPage is the human-facing status page served at "/" until the Flutter
// client and the authenticated API surface land in later phases.
var landingPage = template.Must(template.New("landing").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Yopsa Pamoja — API</title>
<style>
  :root { --blue:#0b4f9c; --green:#0e8a6b; --ink:#101828; --muted:#475467; --line:#d0d5dd; --bg:#f8fafc; }
  * { box-sizing: border-box; }
  body { margin:0; font-family: system-ui, -apple-system, "Segoe UI", Roboto, sans-serif; background:var(--bg); color:var(--ink); }
  header { background:var(--blue); color:#fff; padding:2.5rem 1.5rem; }
  main { max-width: 60rem; margin: -1.5rem auto 3rem; padding: 0 1.5rem; }
  .card { background:#fff; border:1px solid var(--line); border-radius:12px; padding:1.5rem; margin-bottom:1rem; }
  h1 { margin:0 0 .25rem; font-size:1.75rem; }
  h2 { margin:0 0 .75rem; font-size:1.1rem; }
  p { color:var(--muted); line-height:1.55; margin:.35rem 0; }
  .tagline { color:#d6e6f7; margin:0; }
  .chip { display:inline-block; background:#e7f5ef; color:var(--green); border-radius:999px; padding:.2rem .7rem; font-size:.8rem; font-weight:600; }
  dl { display:grid; grid-template-columns: max-content 1fr; gap:.4rem 1.25rem; margin:0; }
  dt { color:var(--muted); }
  dd { margin:0; font-variant-numeric: tabular-nums; }
  code { background:#f2f4f7; padding:.15rem .4rem; border-radius:4px; font-size:.9em; }
  ul { margin:.4rem 0 0; padding-left:1.2rem; color:var(--muted); line-height:1.7; }
  a { color:var(--blue); }
  @media (max-width: 480px) { dl { grid-template-columns: 1fr; } dd { margin-bottom:.4rem; } }
</style>
</head>
<body>
<header>
  <h1>Yopsa Pamoja</h1>
  <p class="tagline">Stronger groups. Informed decisions. — Group management and financial monitoring for Yopsa Microfinance.</p>
</header>
<main>
  <section class="card">
    <h2>Service status</h2>
    <p><span class="chip">API online</span></p>
    <dl>
      <dt>Build</dt><dd>{{.Version}}</dd>
      <dt>Environment</dt><dd>{{.Environment}}</dd>
      <dt>Delivery phase</dt><dd>{{.Phase}}</dd>
    </dl>
  </section>
  <section class="card">
    <h2>Endpoints</h2>
    <ul>
      <li><code>GET /healthz</code> — liveness probe</li>
      <li><code>GET /readyz</code> — readiness probe (database and schema)</li>
      <li><code>GET /api/v1/meta</code> — build and environment metadata</li>
    </ul>
  </section>
  <section class="card">
    <h2>What this build contains</h2>
    <p>The platform skeleton: configuration, structured logging with redaction, security headers, request correlation, health probes and the machine-checked RBAC matrix.</p>
    <p>Authentication, tenant row-level security and the domain modules (registration, meetings, payments, loans, dashboards) follow in P1–P6. The Flutter client follows in P2.</p>
    <p>Documentation lives in the repository's <code>docs/</code> directory; the README is the entry point.</p>
  </section>
</main>
</body>
</html>
`))
