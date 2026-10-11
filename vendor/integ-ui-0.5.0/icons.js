// Small shared controls use the same stroke icons in both adapters.
export const iconPaths = {
 language: '<circle cx="12" cy="12" r="9"/><path d="M3 12h18M12 3a18 18 0 0 1 0 18 18 18 0 0 1 0-18Z"/>',
 appearance: '<circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 1 0 18Z" fill="currentColor" stroke="none"/>',
 account: '<circle cx="12" cy="8" r="3"/><path d="M5 21v-2a7 7 0 0 1 14 0v2"/>',
};
export const iconMarkup = kind => `<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">${iconPaths[kind] || iconPaths.account}</svg>`;

export function controlTitle(slot) {
 const select = slot.querySelector('select');
 if (select) return [select.getAttribute('aria-label'), select.selectedOptions[0]?.textContent].filter(Boolean).join(': ');
 const control = slot.querySelector('a,button,[role="status"]');
 return control?.getAttribute('aria-label') || control?.getAttribute('title') || control?.textContent?.trim() || '';
}
