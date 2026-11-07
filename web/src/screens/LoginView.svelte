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
    <div class="brand">
      <svg width="34" height="34" viewBox="0 0 32 32" aria-hidden="true"><rect width="32" height="32" rx="9" fill="var(--accent)" /><path d="M20.8 10.3 A7.5 7.5 0 1 0 20.8 21.7" fill="none" stroke="var(--accent-text)" stroke-width="3.2" stroke-linecap="round" /><circle cx="17.6" cy="16" r="2.3" fill="var(--accent-text)" /></svg>
      <span>caddy<i>·</i>view</span>
    </div>
    <div>
      <h1>Sign in</h1>
      <p class="lede">to manage your Caddy</p>
    </div>

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
    <p class="hint">Forgot it? Run <code>caddy-view reset-password</code> on the server.</p>
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
    gap: 1.1rem;
    width: min(100%, 25rem);
    padding: 2rem;
    border: 1px solid var(--line2);
    border-radius: var(--radius-lg);
    background: linear-gradient(155deg, var(--accent-soft), transparent 52%), var(--surface);
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    font: 700 1.3rem / 1 var(--display);
    letter-spacing: -0.025em;
  }
  .brand i {
    color: var(--accent);
    font-style: normal;
  }
  h1 {
    margin: 0;
    font: 600 1.9rem / 1.15 var(--display);
    letter-spacing: -0.025em;
  }
  .lede {
    margin: 0.2rem 0 0;
    color: var(--muted);
  }
  label {
    display: grid;
    gap: 0.35rem;
  }
  label span {
    color: var(--muted);
    font: 500 0.72rem / 1.2 var(--mono);
    letter-spacing: 0.08em;
    text-transform: uppercase;
  }
  input {
    padding: 0.65rem 0.8rem;
    border: 1px solid var(--line2);
    border-radius: var(--radius);
    background: var(--surface);
    color: var(--text);
    font: inherit;
  }
  input:focus-visible {
    outline: 2px solid var(--accent);
    outline-offset: 1px;
  }
  button {
    padding: 0.7rem 1rem;
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
    padding: 0.6rem 0.9rem;
    border-left: 3px solid var(--bad);
    border-radius: var(--radius);
    background: var(--bad-soft);
    color: var(--bad);
    font-size: 0.9rem;
  }
  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 0.85rem;
    text-align: center;
  }
</style>
