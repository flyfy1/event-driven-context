import { shellLabels, productGroups, products } from './labels.js';
import { logos } from './logos.js';
export const uiVersion = '0.4.0';
const key = 'integ-ui.appearance';
const escape = value => String(value).replace(/[&<>"']/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[char]));

// Light DOM retains host-owned controls and their routing/auth event handlers.
if (typeof customElements !== 'undefined' && !customElements.get('integ-family-bar')) {
  customElements.define('integ-family-bar', class extends HTMLElement {
    static observedAttributes = ['locale'];
    connectedCallback() {
      this.media = window.matchMedia('(prefers-color-scheme: dark)');
      this.storageListener = event => { if (event.key === key) this.applyTheme(); };
      this.mediaListener = () => this.applyTheme();
      window.addEventListener('storage', this.storageListener);
      this.media.addEventListener('change', this.mediaListener);
      this.render();
    }
    disconnectedCallback() {
      window.removeEventListener('storage', this.storageListener);
      this.media?.removeEventListener('change', this.mediaListener);
    }
    attributeChangedCallback() { if (this.isConnected && this.media) this.render(); }
    readTheme() {
      try { const value = localStorage.getItem(key); if (['system','light','dark'].includes(value)) return value; } catch { /* Storage is optional. */ }
      return this.getAttribute('default-theme') === 'dark' ? 'dark' : 'system';
    }
    applyTheme() {
      if (this.getAttribute("appearance") === "none") return;
      const theme = this.readTheme();
      document.documentElement.dataset.integTheme = theme === 'system' ? (this.media.matches ? 'dark' : 'light') : theme;
      this.querySelector('[data-il-theme]').value = theme;
    }
    render() {
      const locale = this.getAttribute('locale') || 'en';
      const labels = shellLabels(locale);
      const product = this.getAttribute('product') || 'home';
      const info = products.find(item => item.id === product);
      const name = this.getAttribute('name') || info?.name || 'Integ.Life';
      const home = this.getAttribute('home-href') || '/';
      const appearance = this.getAttribute('appearance') !== 'none';
      const actions = this.querySelector('[data-il-actions]');
      actions?.remove();
      this.innerHTML = `<header class="il-family-bar" data-ui-version="${uiVersion}">
        <div class="il-identity"><a class="il-brand" href="${escape(home)}"><img src="${logos[product] || logos.home}" alt="" width="32" height="32"><span><strong>${escape(name)}</strong><small>by Integ.Life</small></span></a><button type="button" class="il-control il-switch" aria-haspopup="dialog" aria-expanded="false" aria-label="${labels.products}" data-il-switch>▦<span>${labels.products}</span></button></div>
        <div class="il-bar-actions">${appearance ? `<label class="il-appearance"><span class="il-sr-only">${labels.appearance}</span><select data-il-theme aria-label="${labels.appearance}"><option value="system">${labels.system}</option><option value="light">${labels.light}</option><option value="dark">${labels.dark}</option></select></label>` : ''}</div>
      </header><dialog class="il-dialog" aria-label="${labels.products}"><div class="il-dialog-head"><h2>${labels.products}</h2><button type="button" class="il-control" data-il-close aria-label="${labels.close}">×</button></div><nav class="il-product-groups" aria-label="${labels.products}">${productGroups(locale).map(group=>`<section class="il-product-group" aria-label="${escape(group.label)}"><h3>${escape(group.label)}</h3><div class="il-product-list">${group.products.map(item=>`<a href="${item.href}" ${item.id===product?'aria-current="page"':''}><img src="${item.logo}" alt="" width="40" height="40"><span class="il-product-copy"><strong>${escape(item.name)}</strong><small>${escape(item.description)}</small></span></a>`).join('')}</div></section>`).join('')}</nav><a class="il-home-link" href="https://integ.life/">${labels.home} ↗</a></dialog>`;
      if (actions) this.querySelector('.il-bar-actions').prepend(actions);
      const dialog = this.querySelector('dialog');
      const trigger = this.querySelector('[data-il-switch]');
      trigger.addEventListener('click', () => { dialog.showModal(); trigger.setAttribute('aria-expanded','true'); });
      dialog.addEventListener('close', () => { trigger.setAttribute('aria-expanded','false'); trigger.focus(); });
      this.querySelector('[data-il-close]').addEventListener('click', () => dialog.close());
      dialog.addEventListener('click', event => {
        if (event.target !== dialog) return;
        const box = dialog.getBoundingClientRect();
        if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) dialog.close();
      });
      this.querySelector('[data-il-theme]')?.addEventListener('change', event => {
        const value = event.target.value;
        try { localStorage.setItem(key,value); } catch { /* Storage is optional. */ }
        document.documentElement.dataset.integTheme = value === 'system' ? (this.media.matches ? 'dark' : 'light') : value;
      });
      this.applyTheme();
    }
  });
}
