<script>
  import { onMount } from 'svelte'
  import { login as apiLogin } from '../lib/api.js'

  let { login = apiLogin, onlogin } = $props()

  let username = $state('admin')
  let password = $state('')
  let error = $state('')
  let busy = $state(false)
  let passwordInput = $state()

  onMount(() => passwordInput?.focus())

  /** @param {number} seconds */
  function waitText(seconds) {
    if (seconds < 60) return `${seconds} second${seconds === 1 ? '' : 's'}`
    const minutes = Math.ceil(seconds / 60)
    return `${minutes} minute${minutes === 1 ? '' : 's'}`
  }

  async function submit(event) {
    event.preventDefault()
    if (busy || !password) return
    busy = true
    error = ''
    try {
      onlogin(await login(username, password))
    } catch (err) {
      error =
        err.status === 429 && err.retryAfter !== undefined
          ? `Too many failed sign-in attempts. Try again in ${waitText(err.retryAfter)}.`
          : err.message
      password = ''
      passwordInput?.focus()
    } finally {
      busy = false
    }
  }
</script>

<main>
  <form onsubmit={submit}>
    <h1>caddy-view</h1>
    <p class="lede">Sign in to continue.</p>

    <label>
      <span>Username</span>
      <input type="text" bind:value={username} autocomplete="username" autocapitalize="none" spellcheck="false" required />
    </label>
    <label>
      <span>Password</span>
      <input type="password" bind:value={password} bind:this={passwordInput} autocomplete="current-password" required />
    </label>

    {#if error}
      <p class="error" role="alert">{error}</p>
    {/if}

    <button type="submit" disabled={busy}>Sign in</button>
  </form>
</main>

<style>
  main {
    display: grid;
    place-items: center;
    min-height: 100vh;
    padding: 1rem;
  }
  form {
    display: grid;
    gap: 1rem;
    width: min(100%, 22rem);
    padding: 2rem;
    border-radius: var(--radius-lg);
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  h1 {
    margin: 0;
    font-size: 1.4rem;
    letter-spacing: -0.01em;
  }
  .lede {
    margin: -0.5rem 0 0;
    color: var(--muted);
  }
  label {
    display: grid;
    gap: 0.35rem;
    font-size: 0.9rem;
  }
  label span {
    color: var(--muted);
  }
  input {
    padding: 0.6rem 0.75rem;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--text);
    font: inherit;
  }
  input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  button {
    padding: 0.65rem 1rem;
    border: 0;
    border-radius: var(--radius);
    background: var(--accent);
    color: var(--accent-text);
    font: inherit;
    font-weight: 600;
    cursor: pointer;
  }
  button:disabled {
    opacity: 0.6;
    cursor: progress;
  }
  .error {
    margin: 0;
    color: var(--bad);
    font-size: 0.9rem;
  }
</style>
