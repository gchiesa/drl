<script>
  import StatusDot from './StatusDot.svelte';

  /** node name/address string */
  export let name = '';
  /** true if this is the current node */
  export let isSelf = false;
  /** 'ok' | 'error' | 'unknown' */
  export let status = 'ok';
  /** optional role label */
  export let role = '';
  /** optional extra metrics object to display */
  export let metrics = null;

  // Extract displayable address (strip port if it's the private API port)
  $: displayName = name || 'Unknown';
</script>

<div class="node-card" class:self={isSelf}>
  <div class="node-header">
    <StatusDot {status} />
    <span class="node-name" title={name}>{displayName}</span>
    {#if isSelf}
      <span class="badge self-badge">this node</span>
    {/if}
    {#if role}
      <span class="badge role-badge">{role}</span>
    {/if}
  </div>

  {#if metrics}
    <div class="node-metrics">
      {#each Object.entries(metrics) as [key, val]}
        <div class="metric-row">
          <span class="metric-key">{key}</span>
          <span class="metric-val">{typeof val === 'number' ? val.toLocaleString() : val}</span>
        </div>
      {/each}
    </div>
  {/if}
</div>

<style>
  .node-card {
    background: var(--bg3);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 12px 14px;
    display: flex;
    flex-direction: column;
    gap: 8px;
    min-width: 200px;
  }
  .node-card.self {
    border-color: var(--accent);
  }
  .node-header {
    display: flex;
    align-items: center;
    gap: 8px;
    flex-wrap: wrap;
  }
  .node-name {
    font-size: 13px;
    font-weight: 600;
    color: var(--text);
    flex: 1;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .badge {
    display: inline-flex;
    align-items: center;
    padding: 1px 6px;
    border-radius: 10px;
    font-size: 10px;
    font-weight: 600;
    flex-shrink: 0;
  }
  .self-badge {
    background: rgba(99,102,241,.2);
    color: var(--accent2);
  }
  .role-badge {
    background: rgba(59,130,246,.15);
    color: var(--blue);
  }
  .node-metrics {
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 2px 8px;
    font-size: 11px;
  }
  .metric-key {
    color: var(--text2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .metric-val {
    color: var(--text);
    text-align: right;
    font-variant-numeric: tabular-nums;
  }
</style>
