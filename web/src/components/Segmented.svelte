<script>
  /** @type {{label: string, options: Array<{value: string, label: string}>, value: string, onchange?: (value: string) => void}} */
  let { label, options, value, onchange = () => {} } = $props()

  // A group of radios, as the keyboard expects it: the chosen one is in the tab order and the arrows move the choice.
  function keydown(event, index) {
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[event.key]
    if (!step) return
    event.preventDefault()
    const next = (index + step + options.length) % options.length
    onchange(options[next].value)
    event.currentTarget.parentElement.children[next]?.focus()
  }
</script>

<div class="segmented" role="radiogroup" aria-label={label}>
  {#each options as option, i (option.value)}
    <button
      type="button"
      role="radio"
      aria-checked={option.value === value}
      tabindex={option.value === value ? 0 : -1}
      onclick={() => option.value !== value && onchange(option.value)}
      onkeydown={(event) => keydown(event, i)}>{option.label}</button
    >
  {/each}
</div>

<style>
  .segmented {
    display: inline-flex;
    gap: 2px;
    padding: 3px;
    border: 1px solid var(--line2);
    border-radius: 999px;
  }
  button {
    padding: 0.3rem 0.9rem;
    border: 0;
    border-radius: 999px;
    background: transparent;
    color: var(--muted);
    font-size: 0.9rem;
  }
  button:hover {
    color: var(--text);
  }
  button[aria-checked='true'] {
    background: var(--text);
    color: var(--bg);
  }
</style>
