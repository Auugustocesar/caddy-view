<script>
  import { untrack } from 'svelte'
  import { createSite, previewSite, updateSite } from '../lib/api.js'
  import { tidyCaddyError } from '../lib/caddy-error.js'
  import { REDIRECT_CODES, emptyForm, errorsByField, formFromSite, payloadFromForm } from '../lib/site-form.js'

  /** @type {{site?: object | null, accessLog?: boolean, previewDelay?: number, onsaved?: (result: object) => void, oncancel?: () => void}} */
  let { site = null, accessLog = false, previewDelay = 400, onsaved = () => {}, oncancel = () => {} } = $props()

  const uid = $props.id()
  const title = site ? `Edit ${site.hostname}` : 'Add a site'

  const CODE_NAMES = { 301: 'Moved Permanently', 302: 'Found', 307: 'Temporary Redirect', 308: 'Permanent Redirect' }

  let form = $state(site ? formFromSite(site) : emptyForm())
  /** A new form is not checked until something has been typed; a site that exists is checked as it opens. */
  let edited = $state(false)
  /** @type {Record<string, boolean>} */
  let touched = $state({})
  let submitted = $state(false)
  /** @type {Record<string, string>} */
  let serverErrors = $state({})
  let failure = $state('')
  let saving = $state(false)
  /** @type {{state: 'idle'|'checking'|'valid'|'invalid'|'failed', text: string, errors: Record<string, string>, message: string}} */
  let check = $state({ state: 'idle', text: '', errors: {}, message: '' })

  // An error shows once its field has been touched, and all of them after a refused submit, so that a form that was just
  // opened is not red from the start.
  const shown = $derived.by(() => {
    /** @type {Record<string, string>} */
    const out = {}
    const all = { ...check.errors, ...serverErrors }
    for (const [field, message] of Object.entries(all)) if (touched[field] || submitted) out[field] = message
    return out
  })
  const alertText = $derived(failure || (check.state === 'failed' ? check.message : ''))
  const canSave = $derived(check.state === 'valid' && !saving)

  function touch(field) {
    touched[field] = true
    edited = true
    failure = ''
    serverErrors = {}
  }

  // Every change is checked with the server: the one implementation of the rules is the server's. A newer change makes an
  // older answer worthless.
  let latest = 0
  $effect(() => {
    const payload = payloadFromForm(form)
    const id = ++latest
    if (!edited && !site) return
    // The text of the last check stays, dimmed, while the next one is made; reading it must not make this effect depend on it.
    check = { state: 'checking', text: untrack(() => check.text), errors: untrack(() => check.errors), message: '' }
    const timer = setTimeout(async () => {
      try {
        const answer = await previewSite(payload, site?.id)
        if (id !== latest) return
        check = answer.valid
          ? { state: 'valid', text: answer.text ?? '', errors: {}, message: '' }
          : { state: 'invalid', text: '', errors: errorsByField(answer.errors), message: '' }
      } catch (err) {
        if (id === latest) check = { state: 'failed', text: '', errors: {}, message: err.message }
      }
    }, previewDelay)
    return () => clearTimeout(timer)
  })

  async function save(event) {
    event.preventDefault()
    if (!canSave) return
    saving = true
    failure = ''
    try {
      const payload = payloadFromForm(form)
      onsaved(site ? await updateSite(site.id, payload) : await createSite(payload))
    } catch (err) {
      failure = tidyCaddyError(err.message)
      if (err.errors) {
        serverErrors = errorsByField(err.errors)
        submitted = true
      }
    } finally {
      saving = false
    }
  }

  const ids = (name) => `${uid}-${name}`
</script>

<form class="wizard" aria-label={title} onsubmit={save}>
  <h2>{title}</h2>

  <div class="field">
    <label for={ids('hostname')}>Hostname</label>
    <input
      id={ids('hostname')}
      type="text"
      placeholder="app.example.com"
      autocomplete="off"
      spellcheck="false"
      bind:value={form.hostname}
      oninput={() => touch('hostname')}
      aria-invalid={shown.hostname ? 'true' : undefined}
      aria-describedby={shown.hostname ? ids('hostname-error') : undefined}
    />
    {#if shown.hostname}<p class="error" id={ids('hostname-error')}>{shown.hostname}</p>{/if}
  </div>

  <fieldset class="field kind">
    <legend>Kind</legend>
    <label class="choice"><input type="radio" name={ids('kind')} value="proxy" bind:group={form.kind} onchange={() => touch('kind')} /> Reverse proxy</label>
    <label class="choice"><input type="radio" name={ids('kind')} value="redirect" bind:group={form.kind} onchange={() => touch('kind')} /> Redirect</label>
    {#if shown.kind}<p class="error">{shown.kind}</p>{/if}
  </fieldset>

  {#if form.kind === 'proxy'}
    <div class="field">
      <label for={ids('upstreams')}>Upstreams</label>
      <textarea
        id={ids('upstreams')}
        rows="3"
        placeholder="10.0.0.5:8080"
        spellcheck="false"
        bind:value={form.upstreams}
        oninput={() => touch('upstreams')}
        aria-invalid={shown.upstreams ? 'true' : undefined}
        aria-describedby={shown.upstreams ? ids('upstreams-error') : ids('upstreams-hint')}
      ></textarea>
      {#if shown.upstreams}
        <p class="error" id={ids('upstreams-error')}>{shown.upstreams}</p>
      {:else}
        <p class="hint" id={ids('upstreams-hint')}>One per line: <code>host:port</code>, or <code>https://host:port</code>.</p>
      {/if}
    </div>
    <div class="field">
      <label class="choice"><input type="checkbox" bind:checked={form.skipTlsVerify} onchange={() => touch('skipTlsVerify')} /> Skip TLS verification</label>
      <p class="hint">For an <code>https://</code> upstream with a certificate nobody signed, such as a self-signed one.</p>
      {#if shown.skipTlsVerify}<p class="error">{shown.skipTlsVerify}</p>{/if}
    </div>
  {:else}
    <div class="field">
      <label for={ids('target')}>Redirect to</label>
      <input
        id={ids('target')}
        type="text"
        placeholder={'https://new.example.com{uri}'}
        spellcheck="false"
        bind:value={form.redirectTarget}
        oninput={() => touch('redirectTarget')}
        aria-invalid={shown.redirectTarget ? 'true' : undefined}
        aria-describedby={shown.redirectTarget ? ids('target-error') : undefined}
      />
      {#if shown.redirectTarget}<p class="error" id={ids('target-error')}>{shown.redirectTarget}</p>{/if}
    </div>
    <div class="field">
      <label for={ids('code')}>Status code</label>
      <select id={ids('code')} bind:value={form.redirectCode} onchange={() => touch('redirectCode')}>
        {#each REDIRECT_CODES as code}
          <option value={code}>{code} {CODE_NAMES[code]}</option>
        {/each}
      </select>
      {#if shown.redirectCode}<p class="error">{shown.redirectCode}</p>{/if}
    </div>
  {/if}

  <div class="field">
    <label for={ids('tls')}>TLS</label>
    <select id={ids('tls')} bind:value={form.tls} onchange={() => touch('tls')}>
      <option value="automatic">Automatic (Caddy gets a certificate)</option>
      <option value="internal">Internal (Caddy's own certificate authority)</option>
      <option value="off">Off (plain HTTP)</option>
    </select>
    {#if shown.tls}<p class="error">{shown.tls}</p>{/if}
  </div>

  <div class="field">
    <label class="choice"><input type="checkbox" bind:checked={form.accessLog} disabled={!accessLog} onchange={() => touch('accessLog')} /> Log requests</label>
    {#if !accessLog}
      <p class="hint">No access log is known to caddy-view, so requests cannot be logged here. Start it with <code>--access-log</code>, or give Caddy a JSON file logger.</p>
    {/if}
    {#if shown.accessLog}<p class="error">{shown.accessLog}</p>{/if}
  </div>

  <div class="field">
    <label class="choice"><input type="checkbox" bind:checked={form.enabled} onchange={() => touch('enabled')} /> Enabled</label>
    <p class="hint">A disabled site stays in the list and is left out of what Caddy runs.</p>
  </div>

  {#if check.state === 'valid' || check.state === 'checking'}
    {#if check.text}
      <div class="generated" aria-busy={check.state === 'checking'}>
        <p class="hint">What caddy-view will write to the managed file for this site set:</p>
        <pre aria-label="Generated Caddyfile">{check.text}</pre>
      </div>
    {/if}
  {/if}

  {#if alertText}
    <div class="failure" role="alert"><p>{alertText}</p></div>
  {/if}

  <div class="actions">
    <button type="submit" class="primary" disabled={!canSave}>{site ? 'Save changes' : 'Add site'}</button>
    <button type="button" onclick={oncancel}>Cancel</button>
  </div>
</form>

<style>
  .wizard {
    display: grid;
    gap: 1rem;
    padding: 1.25rem 1.5rem;
    border-radius: var(--radius-lg);
    background: var(--surface);
    box-shadow: var(--shadow);
  }
  h2 {
    margin: 0;
    font-size: 1.15rem;
    font-weight: 500;
  }
  .field {
    display: grid;
    gap: 0.3rem;
    margin: 0;
    padding: 0;
    border: 0;
    min-width: 0;
  }
  label,
  legend {
    font-size: 0.85rem;
    font-weight: 600;
    padding: 0;
  }
  .choice {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    font-weight: 400;
    font-size: 0.95rem;
  }
  .kind {
    display: flex;
    flex-wrap: wrap;
    gap: 0.3rem 1.5rem;
  }
  .kind legend {
    width: 100%;
    margin-bottom: 0.3rem;
  }
  input[type='text'],
  textarea,
  select {
    width: 100%;
    font: inherit;
    padding: 0.45rem 0.7rem;
    border: 1px solid var(--border);
    border-radius: var(--radius);
    background: var(--bg);
    color: var(--text);
  }
  textarea {
    resize: vertical;
    font-family: var(--mono);
    font-size: 0.9rem;
  }
  [aria-invalid='true'] {
    border-color: var(--bad);
  }
  .hint {
    margin: 0;
    color: var(--muted);
    font-size: 0.8rem;
  }
  .error {
    margin: 0;
    color: var(--bad);
    font-size: 0.85rem;
  }
  .generated pre {
    margin: 0.3rem 0 0;
    padding: 0.75rem 1rem;
    overflow-x: auto;
    border-radius: var(--radius);
    background: var(--surface-2);
    font-family: var(--mono);
    font-size: 0.82rem;
  }
  .generated[aria-busy='true'] pre {
    opacity: 0.55;
  }
  .failure {
    padding: 0.6rem 0.9rem;
    border-radius: var(--radius);
    border-left: 4px solid var(--bad);
    background: var(--surface-2);
  }
  .failure p {
    margin: 0;
    color: var(--bad);
  }
  .actions {
    display: flex;
    gap: 0.5rem;
  }
  .primary {
    border-color: var(--accent);
    background: var(--accent);
    color: var(--accent-text);
    font-weight: 600;
  }
  button:disabled {
    cursor: not-allowed;
    opacity: 0.5;
  }
</style>
