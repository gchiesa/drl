<script>
  import RuleTable from '../components/RuleTable.svelte';

  export let cfgAccounting = null;
  export let cfgMembership = null;
  export let cfgCache = null;
  export let loading = false;

  // Flatten nested objects into key-value pairs for display
  function flattenObj(obj, prefix = '') {
    if (!obj || typeof obj !== 'object') return [];
    return Object.entries(obj).flatMap(([k, v]) => {
      const key = prefix ? `${prefix}.${k}` : k;
      if (v && typeof v === 'object' && !Array.isArray(v)) return flattenObj(v, key);
      return [[key, Array.isArray(v) ? JSON.stringify(v) : String(v ?? '')]];
    });
  }

  // Extract rules array from accounting config
  $: accountingKV = flattenObj(cfgAccounting);

  // Extract rules — check common paths
  $: rawRules = cfgAccounting?.rules
    ?? cfgAccounting?.accounting?.rules
    ?? cfgAccounting?.rate_limit_rules
    ?? [];

  $: rules = Array.isArray(rawRules)
    ? rawRules
    : typeof rawRules === 'object'
      ? Object.entries(rawRules).map(([k, v]) => ({ id: k, ...v }))
      : [];

  $: membershipKV = flattenObj(cfgMembership);
  $: cacheKV = flattenObj(cfgCache);

  function fmtVal(v) {
    if (v === null || v === undefined) return '—';
    return v;
  }
</script>

<div class="config-wrap">
  <!-- Configuration blocks (non-rules) -->
  <div class="section-title">Configuration</div>
  <div class="grid grid-2 mb-4">
    <div class="card">
      <div class="card-title">Accounting Config</div>
      {#if !cfgAccounting}
        <p class="empty">{loading ? 'Loading…' : 'No data'}</p>
      {:else}
        <div class="kv-grid">
          {#each accountingKV.filter(([k]) => k !== 'rules' && !k.startsWith('rules.')) as [key, val]}
            <span class="kv-key">{key}</span>
            <span class="kv-val">{fmtVal(val)}</span>
          {/each}
        </div>
      {/if}
    </div>

    <div class="card">
      <div class="card-title">Membership Config</div>
      {#if !cfgMembership}
        <p class="empty">{loading ? 'Loading…' : 'No data'}</p>
      {:else}
        <div class="kv-grid">
          {#each membershipKV as [key, val]}
            <span class="kv-key">{key}</span>
            <span class="kv-val">{fmtVal(val)}</span>
          {/each}
        </div>
      {/if}
    </div>
  </div>

  {#if cfgCache}
    <div class="card mb-4">
      <div class="card-title">Cache Config</div>
      <div class="kv-grid">
        {#each cacheKV as [key, val]}
          <span class="kv-key">{key}</span>
          <span class="kv-val">{fmtVal(val)}</span>
        {/each}
      </div>
    </div>
  {/if}

  <!-- Rate limit rules (separate section, flat table) -->
  <div class="section-title">Rate Limit Rules</div>
  <div class="card">
    {#if !cfgAccounting}
      <p class="empty">{loading ? 'Loading…' : 'No configuration loaded'}</p>
    {:else if rules.length === 0}
      <p class="empty">No rate limit rules configured</p>
    {:else}
      <RuleTable {rules} />
    {/if}
  </div>
</div>

<style>
  .config-wrap { display: flex; flex-direction: column; }
  .mb-4 { margin-bottom: 16px; }

  .grid { display: grid; gap: 16px; }
  .grid-2 { grid-template-columns: repeat(2, 1fr); }
  @media (max-width: 900px) { .grid-2 { grid-template-columns: 1fr; } }

  .card {
    background: var(--bg2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 20px;
  }
  .card-title {
    font-size: 11px;
    font-weight: 600;
    text-transform: uppercase;
    letter-spacing: 0.8px;
    color: var(--text2);
    margin-bottom: 12px;
  }
  .section-title { font-size: 16px; font-weight: 600; color: var(--text); margin: 4px 0 12px; }

  .kv-grid {
    display: grid;
    grid-template-columns: 200px 1fr;
    gap: 4px 12px;
    font-size: 12px;
    overflow: hidden;
  }
  .kv-key { color: var(--text2); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .kv-val { color: var(--text); font-family: monospace; word-break: break-all; }

  .empty { color: var(--text2); font-size: 13px; padding: 8px 0; }
</style>
