<script>
  /**
   * Displays a flat table of rate-limit rules.
   * @type {Array<object>} rules — array of rule objects from the config API
   */
  export let rules = [];

  let filterText = '';

  // Collect all unique keys across all rules for column headers
  $: allKeys = [...new Set(rules.flatMap(r => Object.keys(r ?? {})))];

  $: filtered = filterText.trim() === ''
    ? rules
    : rules.filter(r =>
        JSON.stringify(r).toLowerCase().includes(filterText.toLowerCase())
      );

  function fmtVal(v) {
    if (v === null || v === undefined) return '—';
    if (typeof v === 'object') return JSON.stringify(v);
    return String(v);
  }
</script>

<div class="rule-table-wrap">
  <div class="toolbar">
    <input
      class="filter-input"
      type="text"
      placeholder="Filter rules…"
      bind:value={filterText}
      aria-label="Filter rules"
    />
    <span class="count">{filtered.length} / {rules.length}</span>
  </div>

  {#if allKeys.length === 0}
    <p class="empty">No rules configured</p>
  {:else}
    <div class="table-scroll">
      <table>
        <thead>
          <tr>
            {#each allKeys as key}
              <th>{key}</th>
            {/each}
          </tr>
        </thead>
        <tbody>
          {#if filtered.length === 0}
            <tr><td colspan={allKeys.length} class="empty">No matching rules</td></tr>
          {:else}
            {#each filtered as rule, i (i)}
              <tr>
                {#each allKeys as key}
                  <td class:mono={typeof rule[key] === 'object'}>{fmtVal(rule[key])}</td>
                {/each}
              </tr>
            {/each}
          {/if}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<style>
  .rule-table-wrap { display: flex; flex-direction: column; gap: 8px; }

  .toolbar { display: flex; gap: 8px; align-items: center; }
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
  .count { font-size: 12px; color: var(--text2); white-space: nowrap; }

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
  .empty { text-align: center; color: var(--text2); padding: 20px; font-size: 13px; }
</style>
