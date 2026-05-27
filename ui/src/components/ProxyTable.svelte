<script>
  /**
   * Displays a flat table of embedded-proxy routes, one row per host×route.
   * @type {object|null} cfg — the embedded-proxy config object from the API
   */
  export let cfg = null;

  // Known duration fields (values are Go time.Duration, i.e. int64 nanoseconds)
  function fmtDuration(ns) {
    const n = Number(ns);
    if (!n || isNaN(n)) return '—';
    const ms = n / 1_000_000;
    if (ms < 1) return `${Math.round(n / 1000)}µs`;
    if (ms < 1000) return `${ms % 1 === 0 ? ms : ms.toFixed(1)}ms`;
    const s = ms / 1000;
    if (s < 60) return `${s % 1 === 0 ? s : s.toFixed(1)}s`;
    const m = Math.floor(s / 60);
    const rem = s % 60;
    return rem === 0 ? `${m}m` : `${m}m ${Math.round(rem)}s`;
  }

  // Flatten hosts[].routes.routes[] into a single array with hostname prepended
  $: rows = (() => {
    if (!cfg?.hosts?.length) return [];
    return cfg.hosts.flatMap(host =>
      (host?.routes?.routes ?? []).map(route => ({
        hostname:              host.hostname ?? '—',
        prefix:                route.prefix ?? '—',
        upstream:              route.upstream ?? '—',
        'balance-strategy':    route['balance-strategy'] || '—',
        'dns-refresh-interval': route['dns-refresh-interval']
          ? fmtDuration(route['dns-refresh-interval'])
          : '—',
        'require-auth':        String(route['require-auth'] ?? false),
      }))
    );
  })();

  const COLUMNS = [
    { key: 'hostname',              label: 'Hostname' },
    { key: 'prefix',                label: 'Prefix' },
    { key: 'upstream',              label: 'Upstream' },
    { key: 'balance-strategy',      label: 'Balance Strategy' },
    { key: 'dns-refresh-interval',  label: 'DNS Refresh' },
    { key: 'require-auth',          label: 'Require Auth' },
  ];
</script>

<div class="proxy-table-wrap">
  {#if rows.length === 0}
    <p class="empty">No proxy routes configured</p>
  {:else}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            {#each COLUMNS as col}
              <th>{col.label}</th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#each rows as row, i (i)}
            <tr>
              {#each COLUMNS as col}
                <td class:mono={col.key === 'upstream'}>{row[col.key]}</td>
              {/each}
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<style>
  .proxy-table-wrap { display: flex; flex-direction: column; gap: 8px; }

  .table-scroll {
    overflow-x: auto;
    border: 1px solid var(--border);
    border-radius: 6px;
  }
  table { width: 100%; border-collapse: collapse; font-size: 12px; white-space: nowrap; }
  th {
    background: var(--bg2);
    text-align: left;
    padding: 8px 10px;
    color: var(--text2);
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.6px;
    border-bottom: 1px solid var(--border);
  }
  td {
    padding: 8px 10px;
    border-bottom: 1px solid rgba(46,50,71,.4);
    color: var(--text);
  }
  tr:last-child td { border-bottom: none; }
  tr:hover td { background: rgba(99,102,241,.04); }
  .mono { font-family: monospace; font-size: 11px; }
  .empty { color: var(--text2); font-size: 13px; padding: 8px 0; }
</style>
