<script>
  /** @type {{label: string, value: string, detail?: string, tone?: 'ok'|'warn'|'bad'}} */
  let { label, value, detail = '', tone = undefined } = $props()

  const labelId = $props.id()
</script>

<div class="tile" role="group" aria-labelledby={labelId} data-tone={tone}>
  <span class="label" id={labelId}>{label}</span><strong class="value">{value}</strong>{#if detail}<span class="detail">{detail}</span>{/if}
</div>

<style>
  .tile {
    display: flex;
    flex-direction: column;
    gap: 0.25rem;
    padding: 1rem 1.15rem;
    border-radius: var(--radius-lg);
    background: var(--surface-2);
  }
  .label {
    color: var(--muted);
    font-size: 0.7rem;
    font-weight: 600;
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .value {
    font-size: 2rem;
    font-weight: 300;
    line-height: 1.15;
    letter-spacing: -0.01em;
    font-variant-numeric: tabular-nums;
  }
  .detail {
    color: var(--muted);
    font-size: 0.8rem;
  }
  @media (max-width: 40rem) {
    .tile {
      padding: 0.85rem 0.95rem;
    }
    .value {
      font-size: 1.65rem;
    }
  }
  .tile[data-tone='bad'] .value {
    color: var(--bad);
  }
  .tile[data-tone='warn'] .value {
    color: var(--warn);
  }
  .tile[data-tone='ok'] .value {
    color: var(--ok);
  }
</style>
