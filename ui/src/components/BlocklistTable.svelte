<script>
  /** @type {Array<{key: string, entity: {ip: string, uriPath: string, headers?: object}, expiresAt?: string}>} */
  export let entries = [];

  let filterText = '';
  let filterCol = 'all';

  const columns = ['all', 'key', 'ip', 'uriPath', 'headers'];

  $: filtered = filterText.trim() === ''
    ? entries
    : entries.filter(e => {
        const q = filterText.toLowerCase();
        if (filterCol === 'all') {
          return [e.key, e.entity?.ip, e.entity?.uriPath, JSON.stringify(e.entity?.headers ?? {})]
            .some(v => (v ?? '').toLowerCase().includes(q));
        }
        if (filterCol === 'key') return (e.key ?? '').toLowerCase().includes(q);
        if (filterCol === 'ip') return (e.entity?.ip ?? '').toLowerCase().includes(q);
        if (filterCol === 'uriPath') return (e.entity?.uriPath ?? '').toLowerCase().includes(q);
        if (filterCol === 'headers') return JSON.stringify(e.entity?.headers ?? {}).toLowerCase().includes(q);
        return true;
      });

  function fmtHeaders(h) {
    if (!h || Object.keys(h).length === 0) return '—';
    return Object.entries(h).map(([k, v]) => `${k}=${v}`).join(', ');
  }

  function fmtExpiry(s) {
    if (!s) return '—';
    try {
      return new Date(s).toLocaleString();
    } catch {
      return s;
    }
  }
</script>

<div class="blocklist-wrap">
  <div class="toolbar">
    <input
      class="filter-input"
      type="text"
      placeholder="Filter…"
      bind:value={filterText}
      aria-label="Filter blocklist"
    />
    <select class="col-select" bind:value={filterCol} aria-label="Filter column">
      {#each columns as col}
        <option value={col}>{col === 'all' ? 'All columns' : col}</option>
      {/each}
    </select>
    <span class="count">{filtered.length} / {entries.length}</span>
  </div>

  <div class="table-scroll">
    <table>
      <thead>
        <tr>
          <th>Key</th>
          <th>IP</th>
          <th>URI Path</th>
          <th>Headers</th>
          <th>Expires At</th>
        </tr>
      </thead>
      <tbody>
        {#if filtered.length === 0}
          <tr><td colspan="5" class="empty">
            {entries.length === 0 ? 'No blocked entities' : 'No results matching filter'}
          </td></tr>
        {:else}
          {#each filtered as entry (entry.key)}
            <tr>
              <td class="mono">{entry.key ?? '—'}</td>
              <td>{entry.entity?.ip ?? '—'}</td>
              <td class="mono">{entry.entity?.uriPath ?? '—'}</td>
              <td class="mono small">{fmtHeaders(entry.entity?.headers)}</td>
              <td class="small">{fmtExpiry(entry.expiresAt)}</td>
            </tr>
          {/each}
        {/if}
      </tbody>
    </table>
  </div>
</div>

<style>
  .blocklist-wrap { display: flex; flex-direction: column; gap: 8px; }

  .toolbar {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  .filter-input {
    flex: 1;
    background: var(--bg3);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 6px 10px;
    color: var(--text);
    font-size: 13px;
    font-family: inherit;
    outline: none;
    min-width: 0;
  }
  .filter-input:focus { border-color: var(--accent); }
  .col-select {
    background: var(--bg3);
    border: 1px solid var(--border);
    border-radius: 6px;
    padding: 6px 8px;
    color: var(--text2);
    font-size: 12px;
    font-family: inherit;
    cursor: pointer;
    outline: none;
  }
  .count { font-size: 12px; color: var(--text2); white-space: nowrap; }

  .table-scroll {
    max-height: 320px;
    overflow-y: auto;
    border: 1px solid var(--border);
    border-radius: 6px;
  }
  table { width: 100%; border-collapse: collapse; font-size: 12px; }
  th {
    position: sticky;
    top: 0;
    background: var(--bg2);
    text-align: left;
    padding: 8px 10px;
    color: var(--text2);
    font-size: 10px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.6px;
    border-bottom: 1px solid var(--border);
    z-index: 1;
  }
  td {
    padding: 8px 10px;
    border-bottom: 1px solid rgba(46,50,71,.4);
    vertical-align: top;
    word-break: break-all;
    color: var(--text);
  }
  tr:last-child td { border-bottom: none; }
  tr:hover td { background: rgba(99,102,241,.04); }
  .mono { font-family: monospace; }
  .small { font-size: 11px; color: var(--text2); }
  .empty { text-align: center; color: var(--text2); padding: 24px; }
</style>
