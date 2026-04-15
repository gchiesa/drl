<script>
  /** 'ok' | 'error' | 'warning' | 'unknown' */
  export let status = 'unknown';
  export let size = 8;

  $: colorMap = {
    ok: 'var(--green)',
    error: 'var(--red)',
    warning: 'var(--yellow)',
    unknown: 'var(--text2)',
  };
  $: color = colorMap[status] ?? colorMap.unknown;
  $: glow = status === 'ok' ? `0 0 6px ${color}` : 'none';
</script>

<span
  class="dot"
  style="width:{size}px;height:{size}px;background:{color};box-shadow:{glow};"
  aria-label={status}
></span>

<style>
  .dot {
    display: inline-block;
    border-radius: 50%;
    flex-shrink: 0;
  }
</style>
