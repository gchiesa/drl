<script>
    import NodeCard from '../components/NodeCard.svelte';
    import MetricsGraph from '../components/MetricsGraph.svelte';
    import BlocklistTable from '../components/BlocklistTable.svelte';
    import StatusDot from '../components/StatusDot.svelte';

    /**
     * @type {{
     *   status: object,
     *   blocked: Array,
     *   accountingStats: object,
     *   metrics: object,
     *   peerMetrics: Array<{nodeId: string, metrics: object}>,
     *   nodeId: string,
     *   clusterName: string,
     * }}
     */
    export let data = null;
    export let error = null;

    // ── Computed values ────────────────────────────────────────────────────────
    $: peers = data?.status?.active_peers ?? [];
    $: peerAddresses = data?.status?.active_peer_addresses ?? [];
    $: sortedPeers = [...peers].sort();
    $: blockedCount = data?.blocked?.length ?? 0;
    $: trackedCount = data?.accountingStats?.monitored_entities_count ?? '—';
    $: uptimeStr = fmtDuration(data?.status?.uptime_seconds);
    $: clusterName = data?.status?.cluster_name ?? data?.clusterName ?? '—';
    $: thisNodeId = data?.nodeId ?? '';

    // ── Aggregated metrics across all nodes ────────────────────────────────────
    $: allMetricsSets = [
        data?.metrics?.metrics ?? {},
        ...(data?.peerMetrics ?? []).map(p => p.metrics ?? {}),
    ];

    $: aggregatedMetrics = allMetricsSets.reduce((acc, m) => {
        for (const [k, v] of Object.entries(m)) {
            acc[k] = (acc[k] ?? 0) + v;
        }
        return acc;
    }, {});

    $: grpcTotal = aggregatedMetrics['drl_grpc_check_total'] ?? 0;
    $: blocksTotal = Object.entries(aggregatedMetrics)
        .filter(([k]) => k.startsWith('drl_ratelimit_blocks_total'))
        .reduce((s, [, v]) => s + v, 0);
    $: tokensConsumed = Object.entries(aggregatedMetrics)
        .filter(([k]) => k.startsWith('drl_ratelimit_tokens_consumed_total'))
        .reduce((s, [, v]) => s + v, 0);
    $: bucketExhausted = Object.entries(aggregatedMetrics)
        .filter(([k]) => k.startsWith('drl_ratelimit_bucket_exhausted_total'))
        .reduce((s, [, v]) => s + v, 0);
    $: tracked = Object.entries(aggregatedMetrics)
        .filter(([k]) => k.startsWith('monitored_entities_count'))
        .reduce((s, [, v]) => s + v, 0);
    $: batcheUpdates = Object.entries(aggregatedMetrics)
        .filter(([k]) => k.startsWith('batched_updates_pending'))
        .reduce((s, [, v]) => s + v, 0);
    $: estimatedEntities = Object.entries(aggregatedMetrics)
        .filter(([k]) => k.startsWith('estimated_entities_count'))
        .reduce((s, [, v]) => s + v, 0);
    $: clusterSize = sortedPeers.length;
    $: handoverSent = aggregatedMetrics['drl_handover_sent_total'] ?? 0;
    $: handoverRecv = aggregatedMetrics['drl_handover_received_total'] ?? 0;

    // ── Helpers ────────────────────────────────────────────────────────────────
    function fmtDuration(s) {
        if (!s && s !== 0) return '—';
        if (typeof s === 'string') return s;
        const h = Math.floor(s / 3600);
        const m = Math.floor((s % 3600) / 60);
        const sec = Math.floor(s % 60);
        return (h ? h + 'h ' : '') + (m ? m + 'm ' : '') + sec + 's';
    }

    function nodeStatus(name) {
        // If we have peer metrics for this node, it's reachable
        if (name === thisNodeId) return 'ok';
        if (data?.peerMetrics?.some(p => p.nodeId === name)) return 'ok';
        return 'unknown';
    }

    $: blockedEntries = (data?.blocked ?? []).map(e => ({
        key: e.id ?? e.key ?? '',
        entity: {
            ip: e.ip ?? '',
            uriPath: e.uri_path ?? e.uriPath ?? '',
            headers: e.headers ?? {},
        },
        expiresAt: e.expires_at ?? e.expiresAt ?? '',
    }));
</script>

{#if error}
    <div class="error-banner">Error loading cluster data: {error}</div>
{/if}

<!-- KPI cards -->
<div class="grid grid-4 mb-4">
    <div class="card">
        <div class="card-title">Cluster Peers</div>
        <div class="stat-value">{peers.length}</div>
        <div class="stat-label">active members</div>
    </div>
    <div class="card">
        <div class="card-title">Blocked Entities</div>
        <div class="stat-value">{blockedCount}</div>
        <div class="stat-label">currently blocked</div>
    </div>
    <div class="card">
        <div class="card-title">Tracked Entities</div>
        <div class="stat-value">{trackedCount}</div>
        <div class="stat-label">in accounting cache</div>
    </div>
    <div class="card">
        <div class="card-title">Uptime</div>
        <div class="stat-value">{uptimeStr}</div>
        <div class="stat-label">this node</div>
    </div>
</div>

<!-- Charts — aggregated across all cluster nodes -->
<p class="aggregate-note mb-4">
    <StatusDot status="ok" size={6}/>
    Charts show aggregated sum across all {allMetricsSets.length} cluster node{allMetricsSets.length !== 1 ? 's' : ''}
</p>
<div class="grid grid-2 mb-4">
    <div class="card">
        <div class="card-title">gRPC Checks (total, all nodes)</div>
        <MetricsGraph label="" color="#6366f1" value={grpcTotal}/>
    </div>
    <div class="card">
        <div class="card-title">Rate Limit Blocks (total, all nodes)</div>
        <MetricsGraph label="" color="#ef4444" value={blocksTotal}/>
    </div>
</div>

<!-- Aggregated metric panels -->
<div class="grid grid-3 mb-4">
    <div class="card">
        <div class="card-title">Accounting (all nodes)</div>
        <div class="kv-grid">
            <span class="kv-key">Tracked</span>
            <span class="kv-val">{tracked}</span>
            <span class="kv-key">Cached (estimated)</span>
            <span class="kv-val">{estimatedEntities}</span>
            <span class="kv-key">Pending Updates</span>
            <span class="kv-val">{batcheUpdates}</span>
        </div>
    </div>
    <div class="card">
        <div class="card-title">Cluster & Handover (all nodes)</div>
        <div class="kv-grid">
            <span class="kv-key">Cluster Size</span>
            <span class="kv-val">{clusterSize}</span>
            <span class="kv-key">Handover Sent</span>
            <span class="kv-val">{handoverSent.toFixed(0)}</span>
            <span class="kv-key">Handover Recv</span>
            <span class="kv-val">{handoverRecv.toFixed(0)}</span>
        </div>
    </div>
    <div class="card">
        <div class="card-title">Token Bucket (all nodes)</div>
        <div class="kv-grid">
            <span class="kv-key">Tokens Consumed</span>
            <span class="kv-val">{tokensConsumed.toFixed(0)}</span>
            <span class="kv-key">Buckets Exhausted</span>
            <span class="kv-val">{bucketExhausted.toFixed(0)}</span>
            <span class="kv-key">gRPC Checks</span>
            <span class="kv-val">{grpcTotal.toFixed(0)}</span>
        </div>
    </div>
</div>

<!-- Cluster peers (sorted) -->
<div class="section-title">Cluster Peers</div>
<div class="card mb-4">
    {#if sortedPeers.length === 0}
        <p class="empty">No peers</p>
    {:else}
        <div class="peer-grid">
            {#each sortedPeers as peer}
                <NodeCard
                        name={peer}
                        isSelf={peer === thisNodeId}
                        status={nodeStatus(peer)}
                />
            {/each}
        </div>
    {/if}
</div>

<!-- Blocked entities (searchable) -->
<div class="section-title">Blocked Entities</div>
<div class="card mb-4">
    <BlocklistTable entries={blockedEntries}/>
</div>

<style>
    .mb-4 {
        margin-bottom: 16px;
    }

    .aggregate-note {
        display: flex;
        align-items: center;
        gap: 6px;
        font-size: 12px;
        color: var(--text2);
        margin-bottom: 8px;
    }

    .grid {
        display: grid;
        gap: 16px;
    }

    .grid-2 {
        grid-template-columns: repeat(2, 1fr);
    }

    .grid-3 {
        grid-template-columns: repeat(3, 1fr);
    }

    .grid-4 {
        grid-template-columns: repeat(4, 1fr);
    }

    @media (max-width: 900px) {
        .grid-4 {
            grid-template-columns: repeat(2, 1fr);
        }

        .grid-3 {
            grid-template-columns: 1fr;
        }

        .grid-2 {
            grid-template-columns: 1fr;
        }
    }

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

    .stat-value {
        font-size: 32px;
        font-weight: 700;
        color: var(--text);
        line-height: 1;
    }

    .stat-label {
        font-size: 12px;
        color: var(--text2);
        margin-top: 4px;
    }

    .section-title {
        font-size: 16px;
        font-weight: 600;
        color: var(--text);
        margin: 4px 0 12px;
    }

    .kv-grid {
        display: grid;
        grid-template-columns: 1fr 1fr;
        gap: 4px 8px;
        font-size: 12px;
    }

    .kv-key {
        color: var(--text2);
    }

    .kv-val {
        color: var(--text);
        text-align: right;
        font-variant-numeric: tabular-nums;
    }

    .peer-grid {
        display: flex;
        flex-wrap: wrap;
        gap: 8px;
    }

    .error-banner {
        background: rgba(239, 68, 68, .1);
        border: 1px solid rgba(239, 68, 68, .3);
        border-radius: 8px;
        padding: 12px 16px;
        color: var(--red);
        font-size: 13px;
        margin-bottom: 16px;
    }

    .empty {
        color: var(--text2);
        font-size: 13px;
        padding: 8px 0;
    }
</style>
