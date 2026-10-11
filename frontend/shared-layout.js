import './vendor/integ-ui-0.4.0/static.js';
const header = document.querySelector('header.header, header.topbar');
if (header) {
  const bar = document.createElement('integ-family-bar');
  bar.setAttribute('product', 'context');
  bar.setAttribute('home-href', './');
  bar.setAttribute('appearance', 'none');
  bar.setAttribute('locale', document.documentElement.lang || 'en');
  const actions = document.createElement('div');
  actions.setAttribute('data-il-actions', '');
  // Move existing nodes so locale, login, logout, and authorization listeners survive.
  for (const child of [...header.children]) {
    if (child.classList.contains('brand')) continue;
    actions.append(child);
  }
  bar.append(actions);
  header.replaceWith(bar);
  new MutationObserver(() => bar.setAttribute('locale', document.documentElement.lang || 'en'))
    .observe(document.documentElement, {attributes:true, attributeFilter:['lang']});
}
