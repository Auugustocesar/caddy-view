<script>
  /** @type {{config?: object | null, loadError?: string}} */
  let { config = null, loadError = '' } = $props()

  const text = $derived(config === null ? '' : JSON.stringify(config, null, 2))
  let feedback = $state('')

  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      feedback = 'Copied'
    } catch {
      feedback = 'Copy failed'
    }
  }

  // The message disappears on its own.
  $effect(() => {
    if (!feedback) return
    const timer = setTimeout(() => (feedback = ''), 2000)
    return () => clearTimeout(timer)
  })
</script>

{#if loadError}
  <p class="error" role="alert">{loadError}</p>
{/if}

{#if config !== null}
  <div class="toolbar">
    <button type="button" onclick={copy}>Copy</button>
    <span aria-live="polite">{feedback}</span>
  </div>
  <pre data-testid="config-json">{text}</pre>
{:else if !loadError}
  <p class="empty">Loading…</p>
{/if}

<style>
  .toolbar {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    margin-bottom: 0.5rem;
    color: var(--muted);
  }
  pre {
    margin: 0;
    padding: 1rem;
    overflow: auto;
    max-height: 75vh;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--surface);
    font-size: 0.85rem;
  }
  .error {
    color: var(--bad);
  }
  .empty {
    color: var(--muted);
  }
</style>
