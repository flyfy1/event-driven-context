(() => {
  const { resolveLocalePreference, normalizeLocale } = window.ContextI18n;
  const dictionaries = window.ContextLandingCopy;
  const localeKey = 'event-context.locale';
  const localeCookie = 'event_context_locale';
  const params = new URLSearchParams(location.search);
  const workspaceViews = new Set(['records', 'state', 'integration', 'plugins']);
  if (params.has('project') && workspaceViews.has(location.hash.slice(1))) {
    const target = new URL('./workspace.html', location.href);
    target.search = location.search;
    target.hash = location.hash;
    location.replace(target.pathname + target.search + target.hash);
    return;
  }
  let saved;
  try { saved = localStorage.getItem(localeKey); } catch { /* Storage may be unavailable. */ }
  function sharedLocale() {
    const prefix = `${localeCookie}=`;
    const value = document.cookie.split(';').map(part => part.trim()).find(part => part.startsWith(prefix));
    if (!value) return '';
    try { return decodeURIComponent(value.slice(prefix.length)); } catch { return ''; }
  }
  let locale = resolveLocalePreference(params.get('locale'), sharedLocale(), saved, navigator.languages || [navigator.language]);
  let selectedQuestion = 0;
  let copyTimer;
  const scenarios = [
    { answer: 'answerBudget', source: 'sourceBudget', records: [1, 2] },
    { answer: 'answerNext', source: 'sourceNext', records: [2, 3] },
    { answer: 'answerGap', source: 'sourceGap', records: [3] }
  ];
  function renderExample() {
    const scenario = scenarios[selectedQuestion];
    document.getElementById('demo-answer').textContent = dictionaries[locale][scenario.answer];
    const source = document.getElementById('demo-source');
    source.textContent = `${dictionaries[locale][scenario.source]} ↗`;
    source.href = `#source-${scenario.records.at(-1)}`;
    document.querySelectorAll('[data-question]').forEach(button => button.setAttribute('aria-pressed', String(Number(button.dataset.question) === selectedQuestion)));
    document.querySelectorAll('.record').forEach((record, i) => record.classList.toggle('relevant', scenario.records.includes(i + 1)));
  }
  function render() {
    const copy = dictionaries[locale];
    document.documentElement.lang = locale;
    document.title = copy.title;
    document.querySelector('meta[name="description"]').content = copy.description;
    document.querySelectorAll('[data-copy]').forEach(element => { element.textContent = copy[element.dataset.copy]; });
    document.getElementById('setup-copy-status').textContent = '';
    document.querySelectorAll('[data-copy-code]').forEach(button => {
      const label = button.closest('.setup-code').querySelector('.setup-code-bar > span').textContent;
      button.setAttribute('aria-label', `${copy.copyCode} · ${label}`);
    });
    const select = document.getElementById('language-select');
    select.value = locale;
    select.setAttribute('aria-label', copy.lang);
    document.querySelector('nav').setAttribute('aria-label', copy.navHow);
    document.querySelector('.questions').setAttribute('aria-label', copy.newChat);
    document.querySelectorAll('[data-workspace]').forEach(link => {
      const target = new URL('./workspace.html', location.href);
      target.searchParams.set('locale', locale);
      if (params.has('api')) target.searchParams.set('api', params.get('api'));
      link.href = target.pathname + target.search;
    });
    renderExample();
  }
  function persist() {
    try { localStorage.setItem(localeKey, locale); } catch { /* Locale remains available in workspace links. */ }
    const attributes = [`${localeCookie}=${encodeURIComponent(locale)}`, 'Max-Age=31536000', 'Path=/', 'SameSite=Lax'];
    if (location.protocol === 'https:') attributes.push('Secure');
    if (location.hostname === 'integ.life' || location.hostname.endsWith('.integ.life')) attributes.push('Domain=.integ.life');
    document.cookie = attributes.join('; ');
  }
  document.getElementById('language-select').addEventListener('change', event => {
    locale = normalizeLocale(event.target.value) || 'en';
    persist();
    const url = new URL(location.href);
    url.searchParams.set('locale', locale);
    history.replaceState(null, '', url);
    render();
  });
  document.querySelectorAll('[data-question]').forEach(button => button.addEventListener('click', () => {
    selectedQuestion = Number(button.dataset.question);
    renderExample();
  }));
  function revealGuide(hash) {
    const guide = document.getElementById(hash.slice(1));
    if (guide?.classList.contains('setup-guide')) guide.open = true;
  }
  document.querySelectorAll('a[href^="#connect-"]').forEach(link => {
    link.addEventListener('click', () => revealGuide(link.hash));
  });
  window.addEventListener('hashchange', () => revealGuide(location.hash));
  document.querySelectorAll('[data-copy-code]').forEach(button => button.addEventListener('click', async () => {
    const code = document.getElementById(button.dataset.copyCode);
    const status = document.getElementById('setup-copy-status');
    clearTimeout(copyTimer);
    status.textContent = '';
    try {
      await navigator.clipboard.writeText(code.textContent);
      status.textContent = dictionaries[locale].copiedCode;
    } catch {
      const selection = window.getSelection();
      const range = document.createRange();
      range.selectNodeContents(code);
      selection.removeAllRanges();
      selection.addRange(range);
      code.parentElement.focus();
      status.textContent = dictionaries[locale].copyFailed;
    }
    copyTimer = setTimeout(() => { status.textContent = ''; }, 5000);
  }));
  persist();
  render();
  revealGuide(location.hash);
})();
