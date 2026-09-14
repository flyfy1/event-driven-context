/* global window, location, document */
// Page routes only: user-entered text, account identifiers and URL queries stay local.
(() => {
  if (location.hostname !== 'context.integ.life' || window.top !== window.self || window.integAnalyticsStarted) return;
  window.integAnalyticsStarted = true;
  const views = new Set(['records', 'files', 'state', 'integration', 'plugins']);
  const safePath = () => {
    if (location.pathname === '/' || location.pathname === '/index.html') return '/';
    if (location.pathname === '/admin/' || location.pathname === '/admin/index.html') return '/admin';
    if (location.pathname === '/workspace.html') {
      const view = location.hash.slice(1).split(/[?&#]/, 1)[0];
      return `/workspace/${views.has(view) ? view : 'records'}`;
    }
    return '/not-found';
  };
  window.dataLayer = window.dataLayer || [];
  function gtag() { window.dataLayer.push(arguments); }
  const page = () => ({page_location: `https://context.integ.life${safePath()}`, page_title: 'Event-driven Context', page_referrer: ''});
  gtag('js', new Date());
  gtag('config', 'G-J7NSTMB704', {...page(), send_page_view: false, allow_google_signals: false, allow_ad_personalization_signals: false});
  let previous;
  const track = () => {
    if (previous === safePath()) return;
    previous = safePath();
    gtag('set', page());
    gtag('event', 'page_view', {...page(), send_to: 'G-J7NSTMB704'});
  };
  for (const method of ['pushState', 'replaceState']) {
    const original = window.history[method];
    window.history[method] = function(...args) { original.apply(this, args); track(); };
  }
  window.addEventListener('popstate', track);
  window.addEventListener('hashchange', track);
  track();
  const script = document.createElement('script');
  script.async = true;
  script.src = 'https://www.googletagmanager.com/gtag/js?id=G-J7NSTMB704';
  document.head.append(script);
})();
