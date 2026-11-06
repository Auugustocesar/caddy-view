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
    gap: 0.3rem;
    min-width: 0;
    padding: 1.1rem 0 0.2rem;
    border-top: 1px solid var(--line2);
  }
  .label {
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  .value {
    font: 600 2.4rem / 1.05 var(--display);
    letter-spacing: -0.03em;
    font-variant-numeric: tabular-nums;
  }
  .detail {
    color: var(--muted);
    font-size: 0.85rem;
  }
  @media (max-width: 40rem) {
    .value {
      font-size: 1.9rem;
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
