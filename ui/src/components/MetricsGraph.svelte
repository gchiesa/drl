<script>
  import { onMount, onDestroy } from 'svelte';

  export let label = '';
  export let color = '#6366f1';
  export let value = 0;
  export let maxHistory = 20;

  let canvas;
  let chart = null;
  const history = [];
  const labels = [];

  onMount(async () => {
    // Import Chart.js (bundled via npm)
    const { Chart, registerables } = await import('chart.js');
    Chart.register(...registerables);

    chart = new Chart(canvas, {
      type: 'line',
      data: {
        labels: [],
        datasets: [{
          label,
          data: [],
          borderColor: color,
          backgroundColor: color + '22',
          borderWidth: 2,
          fill: true,
          tension: 0.3,
          pointRadius: 2,
        }],
      },
      options: {
        responsive: true,
        maintainAspectRatio: false,
        animation: { duration: 300 },
        plugins: {
          legend: { display: false },
          tooltip: { mode: 'index' },
        },
        scales: {
          x: { ticks: { color: '#94a3b8', maxTicksLimit: 6 }, grid: { color: 'rgba(46,50,71,0.5)' } },
          y: { ticks: { color: '#94a3b8' }, grid: { color: 'rgba(46,50,71,0.5)' }, beginAtZero: true },
        },
      },
    });
  });

  // Reactive: update chart whenever value changes
  $: if (chart && value !== undefined) {
    const now = new Date().toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
    history.push(value);
    labels.push(now);
    if (history.length > maxHistory) { history.shift(); labels.shift(); }

    chart.data.labels = [...labels];
    chart.data.datasets[0].data = [...history];
    chart.update('none');
  }

  onDestroy(() => chart?.destroy());
</script>

<div class="graph-wrap">
  {#if label}
    <div class="graph-label">{label}</div>
  {/if}
  <div class="chart-container">
    <canvas bind:this={canvas}></canvas>
  </div>
</div>

<style>
  .graph-wrap { display: flex; flex-direction: column; gap: 4px; }
  .graph-label { font-size: 11px; color: var(--text2); }
  .chart-container { position: relative; height: 160px; }
</style>
