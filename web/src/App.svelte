<script>
  import { onMount } from 'svelte'
  import { getSession, logout, setUnauthorizedHandler } from './lib/api.js'
  import Shell from './Shell.svelte'
  import LoginView from './screens/LoginView.svelte'

  // 'checking' until the server has said whether a login is needed and whether there is a session; then 'login', 'app',
  // or 'error' when it could not be asked. Nothing of the app is created before 'app', so none of its requests is made.
  let phase = $state('checking')
  let user = $state(null)
  let loadError = $state('')

  async function check() {
    phase = 'checking'
    try {
      const session = await getSession()
      user = session.user
      phase = session.required && !session.user ? 'login' : 'app'
    } catch (err) {
      loadError = err.message
      phase = 'error'
    }
  }

  function sessionEnded() {
    user = null
    phase = 'login'
  }

  async function signOut() {
    try {
      await logout()
    } catch {
      // The server cannot be reached, so the session cannot be ended from here; it ends by itself. Leave the app anyway.
    }
    sessionEnded()
  }

  onMount(() => {
    // Any request that finds the session gone sends the user to the login screen.
    setUnauthorizedHandler(sessionEnded)
    check()
    return () => setUnauthorizedHandler(null)
  })
</script>

{#if phase === 'app'}
  <Shell {user} onlogout={signOut} />
{:else if phase === 'login'}
  <LoginView onlogin={(name) => ((user = name), (phase = 'app'))} />
{:else if phase === 'error'}
  <main class="gate">
    <p role="alert">{loadError}</p>
    <button type="button" onclick={check}>Try again</button>
  </main>
{/if}

<style>
  .gate {
    display: grid;
    gap: 1rem;
    justify-items: center;
    margin: 4rem auto;
    padding: 0 1rem;
  }
  .gate p {
    margin: 0;
    color: var(--bad);
  }
</style>
