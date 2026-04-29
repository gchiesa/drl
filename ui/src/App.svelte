<script>
    import {onDestroy, onMount} from 'svelte';
    import {apiFetch, authenticate, authError, authStatus, bootstrapInfo, setBootstrapToken} from './lib/auth.js';
    import ThemeToggle from './components/ThemeToggle.svelte';
    import StatusDot from './components/StatusDot.svelte';
    import ClusterOverview from './views/ClusterOverview.svelte';
    import Configuration from './views/Configuration.svelte';

    // ── Nav ────────────────────────────────────────────────────────────────────
    let activeTab = 'overview'; // 'overview' | 'configuration'

    // ── Data ───────────────────────────────────────────────────────────────────
    let data = null;
    let loadError = null;
    let loading = false;
    let lastUpdated = '';

    // ── Config ─────────────────────────────────────────────────────────────────
    let cfgAccounting = null;
    let cfgMembership = null;

    // ── Token modal ────────────────────────────────────────────────────────────
    let tokenInput = '';
    let tokenSubmitting = false;

    async function handleTokenSubmit() {
        if (!tokenInput.trim()) return;
        tokenSubmitting = true;
        await setBootstrapToken(tokenInput.trim());
        tokenInput = '';
        tokenSubmitting = false;
        if ($authStatus === 'ready') {
            await loadData();
            loadConfig();
        }
    }

    // ── Auto-refresh ───────────────────────────────────────────────────────────
    const REFRESH_OPTIONS = [1, 2, 5, 10, 15, 30];
    let refreshInterval = 30; // seconds
    let refreshTimer = null;

    function scheduleRefresh() {
        clearTimeout(refreshTimer);
        refreshTimer = setTimeout(() => loadData(), refreshInterval * 1000);
    }

    function cancelRefresh() {
        clearTimeout(refreshTimer);
    }

    // ── Derived display values ─────────────────────────────────────────────────
    $: clusterName = data?.status?.cluster_name ?? $bootstrapInfo?.clusterName ?? '—';
    $: nodeId = data?.status?.node_id ?? $bootstrapInfo?.nodeId ?? '—';
    $: clusterStatus = data ? 'ok' : ($authStatus === 'ready' ? 'ok' : 'unknown');

    // ── Data loading ───────────────────────────────────────────────────────────
    async function loadData() {
        if (loading) return;
        loading = true;
        loadError = null;

        try {
            // Parallel: core data fetch
            const [status, blocked, accountingStats] = await Promise.all([
                apiFetch('/status'),
                apiFetch('/blocked-entity').catch(() => []),
                apiFetch('/accounting/stats').catch(() => ({})),
            ]);

            // Own-node metrics
            const metrics = await apiFetch('/drl/ui/api/metrics').catch(() => null);

            // Cross-node aggregation: fetch metrics from all peers via proxy
            const peerMetrics = [];
            const peerAddresses = status?.active_peer_addresses ?? [];
            if (peerAddresses.length > 0) {
                const peerResults = await Promise.allSettled(
                    peerAddresses.map(addr =>
                        apiFetch(`/drl/ui/proxy/${encodeURIComponent(addr)}/drl/ui/api/metrics`)
                    )
                );
                const peerAccountingStats = await Promise.allSettled(
                    peerAddresses.map(addr =>
                        apiFetch(`/drl/ui/proxy/${encodeURIComponent(addr)}/accounting/stats`)
                    )
                );
                for (const r of peerResults) {
                    if (r.status === 'fulfilled' && r.value) {
                        peerMetrics.push(r.value);
                    }
                }
                for (const r of peerAccountingStats) {
                    if (r.status !== 'fulfilled' || !r.value) continue;
                    // Filter the response object to include only numeric properties
                    const filteredMetrics = Object.entries(r.value).reduce((acc, [key, val]) => {
                        if (typeof val === 'number') {
                            acc[key] = val;
                        }
                        return acc;
                    }, {});
                    // Push to peerMetrics only if we found any numeric values
                    if (Object.keys(filteredMetrics).length > 0) {
                        peerMetrics.push({ metrics: filteredMetrics });
                    }
                }
            }

            data = {
                status,
                blocked,
                accountingStats,
                metrics,
                peerMetrics,
                nodeId: metrics?.nodeId ?? nodeId,
                clusterName: status?.cluster_name ?? clusterName,
            };

            lastUpdated = new Date().toLocaleTimeString();
        } catch (err) {
            loadError = err.message || String(err);
        } finally {
            loading = false;
            scheduleRefresh();
        }
    }

    async function loadConfig() {
        [cfgAccounting, cfgMembership] = await Promise.all([
            apiFetch('/configuration/static/accounting').catch(() => null),
            apiFetch('/configuration/static/membership').catch(() => null),
        ]);
    }

    // ── Refresh button ─────────────────────────────────────────────────────────
    async function handleRefresh() {
        cancelRefresh();
        await loadData();
    }

    // ── Tab switching ──────────────────────────────────────────────────────────
    function switchTab(tab) {
        activeTab = tab;
        if (tab === 'configuration' && cfgAccounting === null) {
            loadConfig();
        }
    }

    // ── Lifecycle ──────────────────────────────────────────────────────────────
    onMount(async () => {
        await authenticate();
        if ($authStatus === 'ready') {
            await loadData();
            loadConfig();
        }
    });

    onDestroy(() => cancelRefresh());
</script>

<!-- ── Auth overlay ─────────────────────────────────────────────────────── -->
{#if $authStatus !== 'ready'}
    <div id="auth-overlay">
        <div class="auth-card">
            {#if $authStatus === 'awaiting_token'}
                <div class="token-icon">&#128273;</div>
                <h2>Access Token Required</h2>
                <p>
                    Retrieve your access token out-of-band using Digest authentication, then paste it below:
                </p>
                <code class="token-hint">curl --digest -u "admin:$DRL_PRIVATE_API_KEY" http://&lt;node&gt;:8082/drl/ui/get-token</code>
                <form class="token-form" on:submit|preventDefault={handleTokenSubmit}>
                    <input
                        class="token-input"
                        type="text"
                        placeholder="Paste access token here…"
                        bind:value={tokenInput}
                        disabled={tokenSubmitting}
                        autocomplete="off"
                        spellcheck="false"
                    />
                    <button class="btn" type="submit" disabled={tokenSubmitting || !tokenInput.trim()}>
                        {tokenSubmitting ? 'Connecting…' : 'Connect'}
                    </button>
                </form>
            {:else if $authStatus === 'error'}
                <div class="spinner error-icon">&#10005;</div>
                <h2>Authentication Failed</h2>
                <p>{$authError ?? 'Unknown error'}</p>
                <button class="btn" on:click={authenticate}>Retry</button>
            {:else}
                <div class="spinner"></div>
                <h2>Connecting</h2>
                <p>Establishing secure session…</p>
            {/if}
        </div>
    </div>
{/if}

<!-- ── Header ────────────────────────────────────────────────────────────── -->
<header>
    <div class="header-left">
        <h1>&#9670; DRL Cluster Dashboard</h1>
    </div>
    <nav class="tab-nav">
        <button
                class="tab-btn"
                class:active={activeTab === 'overview'}
                on:click={() => switchTab('overview')}
        >Overview
        </button>
        <button
                class="tab-btn"
                class:active={activeTab === 'configuration'}
                on:click={() => switchTab('configuration')}
        >Configuration
        </button>
    </nav>
    <div class="header-right">
    <span class="meta-item">
      <StatusDot status={clusterStatus}/>
      <span>{clusterName}</span>
    </span>
        <span class="meta-item node-id" title="Node ID">{nodeId}</span>
        {#if lastUpdated}
            <span class="meta-item dim">Updated {lastUpdated}</span>
        {/if}
        <select
                class="refresh-select"
                bind:value={refreshInterval}
                on:change={() => { cancelRefresh(); scheduleRefresh(); }}
                aria-label="Auto-refresh interval"
        >
            {#each REFRESH_OPTIONS as s}
                <option value={s}>{s}s</option>
            {/each}
        </select>
        <button
                class="btn"
                on:click={handleRefresh}
                disabled={loading || $authStatus !== 'ready'}
        >{loading ? 'Loading…' : 'Refresh'}</button>
        <ThemeToggle/>
    </div>
</header>

<!-- ── Main ──────────────────────────────────────────────────────────────── -->
<main>
    {#if activeTab === 'overview'}
        <ClusterOverview
                {data}
                error={loadError}
        />
    {:else if activeTab === 'configuration'}
        <Configuration
                {cfgAccounting}
                {cfgMembership}
                {loading}
        />
    {/if}
</main>

<style>
    :global(*), :global(*::before), :global(*::after) {
        box-sizing: border-box;
        margin: 0;
        padding: 0;
    }

    :global(:root) {
        --bg: #0f1117;
        --bg2: #1a1d27;
        --bg3: #252836;
        --border: #2e3247;
        --text: #e2e8f0;
        --text2: #94a3b8;
        --accent: #6366f1;
        --accent2: #818cf8;
        --green: #22c55e;
        --red: #ef4444;
        --yellow: #f59e0b;
        --blue: #3b82f6;
        --radius: 8px;
        --font: 'Inter', system-ui, sans-serif;
    }

    /* Light mode */
    :global([data-theme="light"]:root), :global([data-theme="light"]) {
        --bg: #f8fafc;
        --bg2: #ffffff;
        --bg3: #f1f5f9;
        --border: #e2e8f0;
        --text: #0f172a;
        --text2: #64748b;
        --accent: #6366f1;
        --accent2: #4f46e5;
    }

    :global(body) {
        font-family: var(--font);
        background: var(--bg);
        color: var(--text);
        min-height: 100vh;
        display: flex;
        flex-direction: column;
    }

    :global(#app) {
        display: flex;
        flex-direction: column;
        min-height: 100vh;
    }

    /* ── Auth overlay ──────────────────────────────────────────────────────── */
    #auth-overlay {
        position: fixed;
        inset: 0;
        background: var(--bg);
        display: flex;
        align-items: center;
        justify-content: center;
        z-index: 999;
    }

    .auth-card {
        background: var(--bg2);
        border: 1px solid var(--border);
        border-radius: 12px;
        padding: 32px;
        max-width: 640px;
        width: 100%;
        text-align: center;
        display: flex;
        flex-direction: column;
        align-items: center;
        gap: 12px;
    }

    .auth-card h2 {
        font-size: 20px;
    }

    .auth-card p {
        color: var(--text2);
        font-size: 14px;
    }

    .spinner {
        display: inline-block;
        width: 36px;
        height: 36px;
        border: 3px solid var(--border);
        border-top-color: var(--accent);
        border-radius: 50%;
        animation: spin 0.8s linear infinite;
    }

    .error-icon {
        border: 2px solid var(--red);
        color: var(--red);
        border-radius: 50%;
        animation: none;
        line-height: 32px;
        font-size: 18px;
    }

    .token-icon {
        font-size: 32px;
        line-height: 1;
    }

    .token-hint {
        display: block;
        background: var(--bg3);
        border: 1px solid var(--border);
        border-radius: 6px;
        padding: 8px 12px;
        font-size: 11px;
        color: var(--text2);
        text-align: left;
        word-break: break-all;
        width: 100%;
    }

    .token-form {
        display: flex;
        flex-direction: column;
        gap: 8px;
        width: 100%;
    }

    .token-input {
        background: var(--bg3);
        border: 1px solid var(--border);
        border-radius: 6px;
        padding: 8px 12px;
        color: var(--text);
        font-size: 13px;
        font-family: monospace;
        width: 100%;
        outline: none;
    }

    .token-input:focus {
        border-color: var(--accent);
    }

    @keyframes spin {
        to {
            transform: rotate(360deg);
        }
    }

    /* ── Header ────────────────────────────────────────────────────────────── */
    header {
        background: var(--bg2);
        border-bottom: 1px solid var(--border);
        padding: 0 24px;
        height: 56px;
        display: flex;
        align-items: center;
        gap: 16px;
        flex-shrink: 0;
    }

    .header-left h1 {
        font-size: 18px;
        font-weight: 700;
        color: var(--accent2);
        letter-spacing: -0.3px;
        white-space: nowrap;
    }

    .tab-nav {
        display: flex;
        gap: 4px;
        flex: 1;
    }

    .tab-btn {
        background: transparent;
        border: none;
        border-radius: 6px;
        padding: 6px 14px;
        font-size: 13px;
        font-family: var(--font);
        color: var(--text2);
        cursor: pointer;
        transition: background 0.15s, color 0.15s;
    }

    .tab-btn:hover {
        background: var(--bg3);
        color: var(--text);
    }

    .tab-btn.active {
        background: var(--bg3);
        color: var(--accent2);
        font-weight: 600;
    }

    .header-right {
        display: flex;
        align-items: center;
        gap: 12px;
        margin-left: auto;
    }

    .meta-item {
        display: flex;
        align-items: center;
        gap: 6px;
        font-size: 12px;
        color: var(--text2);
    }

    .node-id {
        font-family: monospace;
        font-size: 11px;
    }

    .dim {
        color: var(--text2);
    }

    .refresh-select {
        background: var(--bg3);
        border: 1px solid var(--border);
        border-radius: 6px;
        padding: 4px 8px;
        color: var(--text2);
        font-size: 12px;
        font-family: var(--font);
        cursor: pointer;
        outline: none;
    }

    .btn {
        background: var(--accent);
        color: #fff;
        border: none;
        border-radius: 6px;
        padding: 6px 14px;
        font-size: 13px;
        font-family: var(--font);
        cursor: pointer;
        transition: opacity 0.2s;
        white-space: nowrap;
    }

    .btn:hover {
        opacity: 0.85;
    }

    .btn:disabled {
        opacity: 0.5;
        cursor: default;
    }

    /* ── Main ──────────────────────────────────────────────────────────────── */
    main {
        flex: 1;
        padding: 24px;
        max-width: 1400px;
        margin: 0 auto;
        width: 100%;
    }
</style>
